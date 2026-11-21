package audio

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func sine(freq float64, rate int, seconds float64) []float32 {
	n := int(float64(rate) * seconds)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

// zeroCrossingFreq estimates a sine's frequency from rising zero crossings.
func zeroCrossingFreq(s []float32, rate int) float64 {
	var first, last, count int = -1, -1, 0
	for i := 1; i < len(s); i++ {
		if s[i-1] < 0 && s[i] >= 0 {
			if first < 0 {
				first = i
			}
			last = i
			count++
		}
	}
	if count < 2 {
		return 0
	}
	return float64(count-1) * float64(rate) / float64(last-first)
}

func TestWAVRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sine.wav")
	in := sine(440, 44100, 1.0)
	if err := WriteWAV(path, in, 44100); err != nil {
		t.Fatal(err)
	}
	a, err := Decode(path, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if a.Decoder != "wav" || len(a.Samples) != len(in) {
		t.Fatalf("decoder %s, %d samples, want %d", a.Decoder, len(a.Samples), len(in))
	}
	for i := range in {
		if d := math.Abs(float64(a.Samples[i] - in[i])); d > 1e-4 {
			t.Fatalf("sample %d: %v vs %v", i, a.Samples[i], in[i])
		}
	}
}

func TestDecodeWAVResamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sine48k.wav")
	if err := WriteWAV(path, sine(1000, 48000, 1.0), 48000); err != nil {
		t.Fatal(err)
	}
	a, err := Decode(path, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if a.Rate != 44100 || a.SourceRate != 48000 {
		t.Fatalf("rate %d source %d", a.Rate, a.SourceRate)
	}
	if math.Abs(a.Duration()-1.0) > 0.001 {
		t.Fatalf("duration %v", a.Duration())
	}
	if f := zeroCrossingFreq(a.Samples, a.Rate); math.Abs(f-1000) > 1 {
		t.Fatalf("frequency after resampling %v, want 1000", f)
	}
}

func TestResampleRemovesAliasing(t *testing.T) {
	// 30 kHz at 96 kHz is above the 22.05 kHz Nyquist of 44.1 kHz: it must be filtered out.
	out := Resample(sine(30000, 96000, 0.5), 96000, 44100)
	var rms float64
	for _, s := range out[1000 : len(out)-1000] {
		rms += float64(s) * float64(s)
	}
	rms = math.Sqrt(rms / float64(len(out)-2000))
	if rms > 0.01 {
		t.Fatalf("aliased energy rms=%v", rms)
	}
}

func TestDecodeMP3TestSong(t *testing.T) {
	const p = "../../testdata/bonobo-drift.mp3"
	if _, err := os.Stat(p); err != nil {
		t.Skip("test song not present")
	}
	a, err := Decode(p, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if a.Decoder != "mp3" || math.Abs(a.Duration()-270.3) > 0.5 {
		t.Fatalf("decoder %s duration %.2f", a.Decoder, a.Duration())
	}
}

func TestUnknownFormatUsesFFmpeg(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.unknownext")
	if err := os.WriteFile(path, []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(path, 44100); err == nil {
		t.Fatal("expected an error for garbage input")
	}
}

// beatSignal is a deterministic drum-like signal: decaying noise bursts.
func beatSignal(rate int, seconds float64) []float32 {
	out := make([]float32, int(float64(rate)*seconds))
	seed := uint32(1)
	for k := 0.3; k < seconds-0.2; k += 0.5 + 0.07*math.Sin(k) {
		start := int(k * float64(rate))
		for i := 0; i < rate/10 && start+i < len(out); i++ {
			seed = seed*1664525 + 1013904223
			n := float64(seed)/float64(1<<32)*2 - 1
			out[start+i] += float32(0.5 * n * math.Exp(-float64(i)/(0.02*float64(rate))))
		}
	}
	return out
}

func TestMeasureOffset(t *testing.T) {
	ref := beatSignal(44100, 30)
	for _, delay := range []float64{0.037, 0.25, 0} {
		d := int(delay * 44100)
		other := append(make([]float32, d), ref...)
		lag, corr := MeasureOffset(ref, other, 44100, delay+0.02, 0.1)
		if math.Abs(lag-delay) > 0.002 || corr < 0.9 {
			t.Errorf("delay %.3f: measured %.4f (corr %.2f)", delay, lag, corr)
		}
	}
}

func TestEncodeOggPadsAndDecodes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	ref := beatSignal(44100, 10)
	if err := WriteWAV(src, ref, 44100); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.ogg")
	enc, err := EncodeOgg(src, out, 0.123, 5)
	if errors.Is(err, ErrNoEncoder) {
		t.Skip("no sox/ffmpeg")
	}
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode(out, 44100)
	if err != nil {
		t.Fatalf("%s output does not decode: %v", enc, err)
	}
	if a.Decoder != "ogg" {
		t.Fatalf("decoded with %s, want native ogg", a.Decoder)
	}
	lag, corr := MeasureOffset(ref, a.Samples, 44100, 0.123, 0.1)
	if math.Abs(lag-0.123) > 0.002 || corr < 0.9 {
		t.Fatalf("%s: padding measured %.4f s (corr %.2f), want 0.123", enc, lag, corr)
	}
	t.Logf("encoder %s, pad measured %.4f s", enc, lag)
}
