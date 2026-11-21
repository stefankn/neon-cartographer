// Package audio decodes music files to mono float32 PCM at a fixed rate.
package audio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-audio/wav"
	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
)

// Audio is decoded mono PCM.
type Audio struct {
	Samples    []float32 // mono, nominally −1..1
	Rate       int       // samples per second of Samples
	SourceRate int       // sample rate of the input file
	Channels   int       // channel count of the input file
	Decoder    string    // "mp3", "ogg", "wav" or "ffmpeg"
}

// Duration returns the length in seconds.
func (a *Audio) Duration() float64 { return float64(len(a.Samples)) / float64(a.Rate) }

// ErrNoDecoder is returned when the format isn't supported natively and
// ffmpeg isn't available.
var ErrNoDecoder = errors.New("audio: unsupported format and ffmpeg not found on PATH")

// Decode reads path and returns mono audio resampled to rate.
// MP3, OGG Vorbis and WAV are decoded natively; anything else (or a native
// decoder failure) falls back to ffmpeg when it is on PATH.
func Decode(path string, rate int) (*Audio, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var (
		a   *Audio
		err error
	)
	switch ext {
	case ".mp3":
		a, err = decodeMP3(path)
	case ".ogg", ".oga":
		a, err = decodeOgg(path)
	case ".wav", ".wave":
		a, err = decodeWAV(path)
	default:
		err = fmt.Errorf("no native decoder for %q", ext)
	}
	if err != nil {
		native := err
		a, err = decodeFFmpeg(path, rate)
		if err != nil {
			if errors.Is(err, ErrNoDecoder) {
				return nil, fmt.Errorf("audio: %s: %v; %w", path, native, err)
			}
			return nil, fmt.Errorf("audio: %s: native: %v; ffmpeg: %w", path, native, err)
		}
	}
	if len(a.Samples) == 0 {
		return nil, fmt.Errorf("audio: %s: no samples decoded", path)
	}
	if a.Rate != rate {
		a.Samples = Resample(a.Samples, a.Rate, rate)
		a.Rate = rate
	}
	return a, nil
}

// downmix averages interleaved channels into mono.
func downmix(interleaved []float32, channels int) []float32 {
	if channels <= 1 {
		return interleaved
	}
	n := len(interleaved) / channels
	out := make([]float32, n)
	inv := 1 / float32(channels)
	for i := 0; i < n; i++ {
		var s float32
		for c := 0; c < channels; c++ {
			s += interleaved[i*channels+c]
		}
		out[i] = s * inv
	}
	return out
}

func decodeMP3(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d, err := mp3.NewDecoder(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	// go-mp3 always outputs 16-bit little-endian stereo.
	raw, err := io.ReadAll(d)
	if err != nil {
		return nil, err
	}
	n := len(raw) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		l := int16(binary.LittleEndian.Uint16(raw[4*i:]))
		r := int16(binary.LittleEndian.Uint16(raw[4*i+2:]))
		out[i] = (float32(l) + float32(r)) / (2 * 32768)
	}
	return &Audio{Samples: out, Rate: d.SampleRate(), SourceRate: d.SampleRate(), Channels: 2, Decoder: "mp3"}, nil
}

func decodeOgg(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, format, err := oggvorbis.ReadAll(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	return &Audio{
		Samples: downmix(data, format.Channels), Rate: format.SampleRate,
		SourceRate: format.SampleRate, Channels: format.Channels, Decoder: "ogg",
	}, nil
}

func decodeWAV(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := wav.NewDecoder(f)
	if !d.IsValidFile() {
		return nil, errors.New("not a valid WAV file")
	}
	if d.WavAudioFormat != 1 {
		return nil, fmt.Errorf("WAV format %d not supported natively (only PCM)", d.WavAudioFormat)
	}
	buf, err := d.FullPCMBuffer()
	if err != nil {
		return nil, err
	}
	scale := float32(math.Pow(2, float64(buf.SourceBitDepth-1)))
	if buf.SourceBitDepth == 8 { // 8-bit WAV is unsigned
		for i := range buf.Data {
			buf.Data[i] -= 128
		}
	}
	fl := make([]float32, len(buf.Data))
	for i, v := range buf.Data {
		fl[i] = float32(v) / scale
	}
	ch := buf.Format.NumChannels
	return &Audio{
		Samples: downmix(fl, ch), Rate: buf.Format.SampleRate,
		SourceRate: buf.Format.SampleRate, Channels: ch, Decoder: "wav",
	}, nil
}

func decodeFFmpeg(path string, rate int) (*Audio, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, ErrNoDecoder
	}
	cmd := exec.Command(bin, "-v", "error", "-i", path, "-f", "f32le", "-ac", "1", "-ar", strconv.Itoa(rate), "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	raw := stdout.Bytes()
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return &Audio{Samples: out, Rate: rate, SourceRate: rate, Channels: 1, Decoder: "ffmpeg"}, nil
}

// WriteWAV writes mono samples as 16-bit PCM WAV (clipped to −1..1).
func WriteWAV(path string, samples []float32, rate int) error {
	var buf bytes.Buffer
	dataLen := uint32(len(samples) * 2)
	w := func(v any) { binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(36 + dataLen))
	buf.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	buf.WriteString("data")
	w(dataLen)
	for _, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		w(int16(math.Round(float64(s) * 32767)))
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// MixClicks returns samples (scaled to 0.6) with a short click at every
// time in times; clicks where accent[i] is true are higher and louder.
// Used to check a beat grid by ear.
func MixClicks(samples []float32, rate int, times []float64, accent []bool) []float32 {
	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = 0.6 * s
	}
	n := int(0.03 * float64(rate))
	for k, t := range times {
		freq, amp := 1500.0, 0.35
		if k < len(accent) && accent[k] {
			freq, amp = 2500.0, 0.5
		}
		start := int(t * float64(rate))
		for i := 0; i < n && start+i < len(out); i++ {
			if start+i < 0 {
				continue
			}
			x := float64(i) / float64(rate)
			out[start+i] += float32(amp * math.Sin(2*math.Pi*freq*x) * math.Exp(-x/0.008))
		}
	}
	return out
}
