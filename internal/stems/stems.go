// Package stems separates a song into vocals, drums, bass and other with the
// optional external tool demucs, caches the stems next to the song and loads
// them aligned with the mix.
//
// demucs decodes with ffmpeg, so its stems share ffmpeg's timeline, which
// differs from the native MP3 decoder's by tens of ms (encoder delay). When
// stems are used, the mix is therefore decoded with ffmpeg as well, and the
// stems' offset is measured as a check.
package stems

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
)

// Names are the four demucs stems.
var Names = []string{"vocals", "drums", "bass", "other"}

// EnvDemucs names the environment variable that points to the demucs
// executable. If it is set, nothing else is searched.
const EnvDemucs = "NCART_DEMUCS"

// repoDemucs is where `just setup-stems` installs demucs, relative to the
// repository root.
var repoDemucs = filepath.Join("tools", "stems", ".venv", "bin", "demucs")

// ErrNoDemucs is returned when demucs can't be found.
var ErrNoDemucs = errors.New("demucs not found (install it with `just setup-stems`, or set " + EnvDemucs + ")")

// Stems are a song's four stems, mono at the analysis rate, on the mix's timeline.
type Stems struct {
	Dir                        string
	Vocals, Drums, Bass, Other *audio.Audio
	// OffsetSec is the measured lag of the stems against the mix before any
	// correction (≈ 0 expected), Corr the envelope correlation of their sum
	// with the mix.
	OffsetSec, Corr float64
	// Shifted: the stems were moved by OffsetSec to line up with the mix.
	Shifted bool
	// Separated: demucs ran in this call (otherwise the stems were cached).
	Separated   bool
	Device      string  // demucs device, if Separated
	SeparateSec float64 // demucs run time, if Separated
}

// Dir is the cache directory of song's stems: <song>.stems/<model>.
func Dir(song string, cfg config.StemsConfig) string {
	return filepath.Join(song+".stems", cfg.Model)
}

// Cached reports whether all four stems of song are in the cache.
func Cached(song string, cfg config.StemsConfig) bool {
	dir := Dir(song, cfg)
	for _, n := range Names {
		if fi, err := os.Stat(filepath.Join(dir, n+".wav")); err != nil || fi.Size() == 0 {
			return false
		}
	}
	return true
}

// Demucs finds the demucs executable: $NCART_DEMUCS if set; else
// tools/stems/.venv/bin/demucs in the working directory or one of its
// parents (the repository); else demucs on PATH.
func Demucs() (string, error) {
	if p := os.Getenv(EnvDemucs); p != "" {
		if !executable(p) {
			return "", fmt.Errorf("%s=%s is not an executable file", EnvDemucs, p)
		}
		return p, nil
	}
	if dir, err := os.Getwd(); err == nil {
		for {
			if p := filepath.Join(dir, repoDemucs); executable(p) {
				return p, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if p, err := exec.LookPath("demucs"); err == nil {
		return p, nil
	}
	return "", ErrNoDemucs
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// SeparateOptions control a demucs run.
type SeparateOptions struct {
	// Device is "auto" (Apple GPU if torch can use it, else CPU), "cpu" or "mps".
	Device string
	// Progress receives demucs' progress output (nil: discarded).
	Progress io.Writer
}

// Separate runs demucs on song and writes the four stems to Dir(song),
// replacing any cached ones. It returns the device used.
//
// --shifts 0: demucs' default applies one random time shift, so every run
// gives different stems; with 0, runs on the same device are bit-identical.
func Separate(song string, cfg config.StemsConfig, o SeparateOptions) (string, error) {
	bin, err := Demucs()
	if err != nil {
		return "", err
	}
	device := o.Device
	if device == "" || device == "auto" {
		device = autoDevice(bin)
	}
	cmd := exec.Command(bin, "-n", cfg.Model, "-d", device, "--shifts", "0",
		"-o", song+".stems", "--filename", "{stem}.{ext}", song)
	var tail bytes.Buffer
	out := io.Writer(&tail)
	if o.Progress != nil {
		out = io.MultiWriter(o.Progress, &tail)
	}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(tail.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:] // demucs ends with the exception line
		}
		return device, fmt.Errorf("demucs on %s: %v: %s", device, err, msg)
	}
	if !Cached(song, cfg) {
		return device, fmt.Errorf("demucs finished but %s doesn't hold all four stems", Dir(song, cfg))
	}
	return device, nil
}

// autoDevice asks the Python next to demucs whether torch can use the Apple
// GPU. Inside a sandbox without Metal access (nono) it can't, and the CPU is
// used; if the check fails for any reason, too.
func autoDevice(demucsBin string) string {
	py := filepath.Join(filepath.Dir(demucsBin), "python")
	out, err := exec.Command(py, "-c",
		"import torch; print('mps' if torch.backends.mps.is_available() else 'cpu')").Output()
	if err == nil && strings.TrimSpace(string(out)) == "mps" {
		return "mps"
	}
	return "cpu"
}

// Mode selects whether Open uses stems.
type Mode int

const (
	// Auto uses cached stems, or separates the song if demucs is installed.
	Auto Mode = iota
	// Off never uses stems (-no-stems).
	Off
)

// Opened is the result of Open.
type Opened struct {
	Mix   *audio.Audio
	Stems *Stems // nil: no stems; the mix was decoded as without stems
	// Note says why there are no stems, if that's expected (off, demucs not
	// installed); Warnings report problems that made Open fall back.
	Note     string
	Warnings []string
}

// Open decodes song for analysis and, unless mode is Off, loads its stems:
// cached ones, or freshly separated if demucs is installed. With stems, the
// mix is decoded by ffmpeg (the stems' timeline). Whenever stems end up
// unused, the mix is decoded exactly as without stems (audio.Decode), so a
// failure never changes the result compared with -no-stems. Only decoding
// the song itself can fail.
func Open(song string, rate int, cfg config.StemsConfig, mode Mode, progress io.Writer) (*Opened, error) {
	o := &Opened{}
	st, err := o.load(song, rate, cfg, mode, progress)
	if err != nil {
		o.Warnings = append(o.Warnings, "stems not used: "+err.Error())
		st = nil
	}
	if st == nil {
		mix, err := audio.Decode(song, rate)
		if err != nil {
			return nil, err
		}
		o.Mix = mix
		return o, nil
	}
	o.Stems = st
	return o, nil
}

// load returns the stems and sets o.Mix, or returns (nil, nil) with o.Note
// when stems are expectedly unavailable, or an error for a real problem.
func (o *Opened) load(song string, rate int, cfg config.StemsConfig, mode Mode, progress io.Writer) (*Stems, error) {
	if mode == Off {
		o.Note = "off (-no-stems)"
		return nil, nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if Cached(song, cfg) {
			return nil, errors.New("ffmpeg not found (needed to decode the song on the stems' timeline)")
		}
		o.Note = "none (needs ffmpeg and demucs)"
		return nil, nil
	}
	st := &Stems{Dir: Dir(song, cfg)}
	if !Cached(song, cfg) {
		if _, err := Demucs(); err != nil {
			if errors.Is(err, ErrNoDemucs) {
				o.Note = "none (" + err.Error() + ")"
			} else {
				o.Note = "none (demucs unavailable: " + err.Error() + ")"
			}
			return nil, nil
		}
		if progress != nil {
			fmt.Fprintf(progress, "Separating stems with demucs (one-off per song, ≈ 1–2 min) …\n")
		}
		t0 := time.Now()
		dev, err := Separate(song, cfg, SeparateOptions{Device: "auto", Progress: progress})
		if err != nil {
			return nil, err
		}
		st.Separated, st.Device, st.SeparateSec = true, dev, time.Since(t0).Seconds()
	}

	mix, err := audio.DecodeFFmpeg(song, rate)
	if err != nil {
		return nil, err
	}
	dst := []**audio.Audio{&st.Vocals, &st.Drums, &st.Bass, &st.Other}
	for i, n := range Names {
		// ffmpeg: ≈ 0.4 s per stem vs ≈ 12 s with the native WAV decoder
		// (go-audio/wav reads sample by sample); ffmpeg is present here.
		a, err := audio.DecodeFFmpeg(filepath.Join(st.Dir, n+".wav"), rate)
		if err != nil {
			return nil, err
		}
		if d := a.Duration() - mix.Duration(); math.Abs(d) > cfg.MaxDurationDiffSec {
			return nil, fmt.Errorf("%s is %.2f s long, the song %.2f s: stale stems? delete %s and run again",
				n, a.Duration(), mix.Duration(), filepath.Dir(st.Dir))
		}
		*dst[i] = a
	}

	// The stems add up to the mix: measure their offset on the sum.
	sum := make([]float32, len(mix.Samples))
	for _, p := range dst {
		for i, v := range (*p).Samples[:min(len(sum), len((*p).Samples))] {
			sum[i] += v
		}
	}
	st.OffsetSec, st.Corr = audio.MeasureOffset(mix.Samples, sum, rate, 0, cfg.MaxSearchSec)
	if st.Corr < cfg.MinAlignCorr {
		return nil, fmt.Errorf("stems don't match the song (correlation %.2f): stale stems? delete %s and run again",
			st.Corr, filepath.Dir(st.Dir))
	}
	if math.Abs(st.OffsetSec) > cfg.MaxOffsetSec {
		for _, p := range dst {
			(*p).Samples = shift((*p).Samples, -st.OffsetSec, rate)
		}
		st.Shifted = true
		o.Warnings = append(o.Warnings, fmt.Sprintf("stems were %+.1f ms off the song and have been shifted", st.OffsetSec*1000))
	}
	o.Mix = mix
	return st, nil
}

// shift moves samples by sec (positive: later), keeping the length.
func shift(s []float32, sec float64, rate int) []float32 {
	n := int(math.Round(sec * float64(rate)))
	out := make([]float32, len(s))
	switch {
	case n >= 0:
		if n < len(s) {
			copy(out[n:], s[:len(s)-n])
		}
	default:
		if -n < len(s) {
			copy(out, s[-n:])
		}
	}
	return out
}
