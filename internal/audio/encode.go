package audio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNoEncoder is returned when neither sox nor ffmpeg is available.
var ErrNoEncoder = errors.New("audio: no Ogg Vorbis encoder found: install sox (recommended: brew install sox) or ffmpeg")

// Encoder names reported by EncodeOgg.
const (
	EncoderSox          = "sox (libvorbis)"
	EncoderFFmpegLib    = "ffmpeg (libvorbis)"
	EncoderFFmpegNative = "ffmpeg (native vorbis, experimental)"
)

// EncodeOgg converts the audio file in to Ogg Vorbis at out, at 44.1 kHz,
// keeping its channels and prepending padSec seconds of silence. quality is
// the Vorbis quality level (0–10, 6 ≈ 192 kbit/s). Encoders are tried in
// order: sox, ffmpeg with libvorbis, ffmpeg's native (lower quality) encoder.
func EncodeOgg(in, out string, padSec float64, quality int) (string, error) {
	if padSec < 0 {
		return "", fmt.Errorf("audio: negative padding %v", padSec)
	}
	padSamples := int(math.Round(padSec * 44100))
	var errs []string
	if sox, err := exec.LookPath("sox"); err == nil {
		cmd := exec.Command(sox, "-V1", "-G", in, "-r", "44100", "-C", strconv.Itoa(quality), out,
			"pad", strconv.Itoa(padSamples)+"s")
		if err := run(cmd); err == nil {
			return EncoderSox, nil
		} else {
			errs = append(errs, "sox: "+err.Error())
		}
	}
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		codec, name, extra := "libvorbis", EncoderFFmpegLib, []string{}
		if !ffmpegHasEncoder(ff, "libvorbis") {
			codec, name, extra = "vorbis", EncoderFFmpegNative, []string{"-strict", "-2", "-ac", "2"}
		}
		args := []string{"-y", "-v", "error", "-i", in, "-vn", "-ar", "44100",
			"-af", fmt.Sprintf("adelay=%dS:all=1", padSamples), "-c:a", codec, "-q:a", strconv.Itoa(quality)}
		args = append(append(args, extra...), out)
		if err := run(exec.Command(ff, args...)); err == nil {
			return name, nil
		} else {
			errs = append(errs, "ffmpeg: "+err.Error())
		}
	}
	if len(errs) == 0 {
		return "", ErrNoEncoder
	}
	return "", fmt.Errorf("audio: encoding to Ogg failed: %s", strings.Join(errs, "; "))
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func ffmpegHasEncoder(ff, name string) bool {
	out, err := exec.Command(ff, "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}

// Tags holds song metadata read from the file.
type Tags struct {
	Title  string
	Artist string
}

// ProbeTags reads title and artist tags with ffprobe. Missing ffprobe or
// tags give empty strings.
func ProbeTags(path string) Tags {
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		return Tags{}
	}
	out, err := exec.Command(fp, "-v", "error", "-show_entries", "format_tags", "-of", "json", path).Output()
	if err != nil {
		return Tags{}
	}
	var res struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &res) != nil {
		return Tags{}
	}
	var t Tags
	for k, v := range res.Format.Tags {
		switch strings.ToLower(k) {
		case "title":
			t.Title = strings.TrimSpace(v)
		case "artist":
			t.Artist = strings.TrimSpace(v)
		}
	}
	return t
}

// ExtractCover returns the file's embedded cover art as a PNG of at most
// size×size pixels, using ffmpeg. ok is false if there is none.
func ExtractCover(path string, size int) (png []byte, ok bool) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, false
	}
	cmd := exec.Command(ff, "-v", "error", "-i", path, "-an", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", size, size),
		"-c:v", "png", "-f", "image2pipe", "-")
	out, err := cmd.Output()
	if err != nil || !bytes.HasPrefix(out, []byte("\x89PNG")) {
		return nil, false
	}
	return out, true
}
