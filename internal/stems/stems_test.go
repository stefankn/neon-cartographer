package stems

import (
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
)

const rate = 44100

// parts builds four deterministic stem signals (kicks, bass tone, noise
// bursts, a slow melody) of the given length; the song is their sum.
func parts(seconds float64) map[string][]float32 {
	n := int(seconds * rate)
	p := map[string][]float32{}
	for _, name := range Names {
		p[name] = make([]float32, n)
	}
	rng := rand.New(rand.NewSource(7))
	for k := 0; ; k++ {
		t0 := 0.25 + float64(k)*0.5 // 120 BPM
		if t0 > seconds-0.5 {
			break
		}
		st := int(t0 * rate)
		ph := 0.0
		for i := 0; i < rate/3 && st+i < n; i++ {
			x := float64(i) / rate
			ph += 2 * math.Pi * (55 + 70*math.Exp(-x/0.02)) / rate
			p["drums"][st+i] += float32(0.5 * math.Sin(ph) * math.Exp(-x/0.12))
		}
		if k%2 == 1 { // bass note on every other beat
			for i := 0; i < rate/2 && st+i < n; i++ {
				x := float64(i) / rate
				p["bass"][st+i] += float32(0.2 * math.Sin(2*math.Pi*82*x) * math.Exp(-x/0.3))
			}
		}
		hs := int((t0 + 0.25) * rate)
		for i := 0; i < rate/12 && hs+i < n; i++ {
			p["other"][hs+i] += float32(0.1 * (2*rng.Float64() - 1) * math.Exp(-float64(i)/(0.02*rate)))
		}
	}
	for i := range n {
		x := float64(i) / rate
		f := 440 * math.Pow(2, float64(int(x/2)%4)/12)
		p["vocals"][i] = float32(0.08 * math.Sin(2*math.Pi*f*x) * (0.5 + 0.5*math.Sin(2*math.Pi*0.25*x)))
	}
	return p
}

func sum(p map[string][]float32) []float32 {
	var out []float32
	for _, s := range p {
		if out == nil {
			out = make([]float32, len(s))
		}
		for i, v := range s {
			out[i] += v
		}
	}
	return out
}

func writeWAV(t *testing.T, path string, s []float32) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := audio.WriteWAV(path, s, rate); err != nil {
		t.Fatal(err)
	}
}

// writeStems writes p as the stems in dir, each moved later by delaySec.
func writeStems(t *testing.T, dir string, p map[string][]float32, delaySec float64) {
	t.Helper()
	for name, s := range p {
		writeWAV(t, filepath.Join(dir, name+".wav"), shift(s, delaySec, rate))
	}
}

// song writes a 12 s test song and returns its path and stem parts.
func song(t *testing.T) (string, map[string][]float32) {
	t.Helper()
	p := parts(12)
	path := filepath.Join(t.TempDir(), "song.wav")
	writeWAV(t, path, sum(p))
	return path, p
}

func script(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeDemucs installs a demucs stand-in that records its arguments to
// $dir/args and copies the WAVs in stemsDir to its -o directory.
func fakeDemucs(t *testing.T, stemsDir string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "demucs"), filepath.Join(dir, "args")
	script(t, bin, `echo "$@" > '`+argsFile+`'
out=""; model=""
while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift;; -n) model="$2"; shift;; esac; shift; done
mkdir -p "$out/$model" && cp '`+stemsDir+`'/*.wav "$out/$model/"
`)
	t.Setenv(EnvDemucs, bin)
	return bin, argsFile
}

func needFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found")
	}
}

func TestCached(t *testing.T) {
	cfg := config.Default().Stems
	songPath := filepath.Join(t.TempDir(), "a.mp3")
	if got := Dir(songPath, cfg); got != songPath+".stems/htdemucs" {
		t.Fatalf("Dir = %s", got)
	}
	if Cached(songPath, cfg) {
		t.Fatal("cached without files")
	}
	one := []float32{0, 0.1, 0}
	for _, n := range Names[:3] {
		writeWAV(t, filepath.Join(Dir(songPath, cfg), n+".wav"), one)
	}
	if Cached(songPath, cfg) {
		t.Fatal("cached with three of four stems")
	}
	last := filepath.Join(Dir(songPath, cfg), Names[3]+".wav")
	if err := os.WriteFile(last, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if Cached(songPath, cfg) {
		t.Fatal("cached with an empty stem file")
	}
	writeWAV(t, last, one)
	if !Cached(songPath, cfg) {
		t.Fatal("not cached with all four stems")
	}
}

func TestDemucsLookup(t *testing.T) {
	// $NCART_DEMUCS wins, and must exist
	bin := filepath.Join(t.TempDir(), "my-demucs")
	script(t, bin, "exit 0\n")
	t.Setenv(EnvDemucs, bin)
	if got, err := Demucs(); err != nil || got != bin {
		t.Fatalf("env: %q, %v", got, err)
	}
	t.Setenv(EnvDemucs, filepath.Join(t.TempDir(), "missing"))
	if _, err := Demucs(); err == nil {
		t.Fatal("nonexistent $NCART_DEMUCS accepted")
	}

	// the repository's venv, found from a subdirectory
	t.Setenv(EnvDemucs, "")
	repo := t.TempDir()
	venv := filepath.Join(repo, repoDemucs)
	script(t, venv, "exit 0\n")
	sub := filepath.Join(repo, "internal", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got, err := Demucs(); err != nil || got != venv {
		t.Fatalf("repo venv: %q, %v", got, err)
	}
}

func TestSeparate(t *testing.T) {
	cfg := config.Default().Stems
	songPath, p := song(t)
	src := t.TempDir()
	writeStems(t, src, p, 0)
	bin, argsFile := fakeDemucs(t, src)

	dev, err := Separate(songPath, cfg, SeparateOptions{Device: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if dev != "cpu" { // no python next to the fake demucs
		t.Errorf("auto device %q without python, want cpu", dev)
	}
	args, _ := os.ReadFile(argsFile)
	want := "-n htdemucs -d cpu --shifts 0 -o " + songPath + ".stems --filename {stem}.{ext} " + songPath
	if got := strings.TrimSpace(string(args)); got != want {
		t.Errorf("demucs args\n got %s\nwant %s", got, want)
	}
	if !Cached(songPath, cfg) {
		t.Error("stems not cached after Separate")
	}

	// auto picks mps when the venv's torch reports it
	script(t, filepath.Join(filepath.Dir(bin), "python"), "echo mps\n")
	if dev, err := Separate(songPath, cfg, SeparateOptions{Device: "auto"}); err != nil || dev != "mps" {
		t.Errorf("auto device %q (%v), want mps", dev, err)
	}
	if dev, err := Separate(songPath, cfg, SeparateOptions{Device: "cpu"}); err != nil || dev != "cpu" {
		t.Errorf("forced device %q (%v), want cpu", dev, err)
	}

	// a failing demucs reports its last line
	script(t, bin, "echo progress; echo 'RuntimeError: boom' >&2; exit 1\n")
	if _, err := Separate(songPath, cfg, SeparateOptions{Device: "cpu"}); err == nil || !strings.Contains(err.Error(), "RuntimeError: boom") {
		t.Errorf("failure: %v", err)
	}
}

func TestOpenCached(t *testing.T) {
	needFFmpeg(t)
	cfg := config.Default().Stems
	t.Setenv(EnvDemucs, filepath.Join(t.TempDir(), "missing")) // must not be needed
	songPath, p := song(t)
	writeStems(t, Dir(songPath, cfg), p, 0)

	o, err := Open(songPath, rate, cfg, Auto, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stems == nil {
		t.Fatalf("no stems: note %q, warnings %v", o.Note, o.Warnings)
	}
	st := o.Stems
	if o.Mix.Decoder != "ffmpeg" || len(o.Warnings) > 0 || st.Separated || st.Shifted {
		t.Errorf("decoder %s, warnings %v, separated %v, shifted %v", o.Mix.Decoder, o.Warnings, st.Separated, st.Shifted)
	}
	if math.Abs(st.OffsetSec) > 0.001 || st.Corr < 0.9 {
		t.Errorf("offset %.2f ms, corr %.2f", st.OffsetSec*1000, st.Corr)
	}
	for _, a := range []*audio.Audio{st.Vocals, st.Drums, st.Bass, st.Other} {
		if a == nil || a.Rate != rate || math.Abs(a.Duration()-o.Mix.Duration()) > 0.01 {
			t.Fatalf("stem %+v", a)
		}
	}
}

func TestOpenShiftsLateStems(t *testing.T) {
	needFFmpeg(t)
	cfg := config.Default().Stems
	t.Setenv(EnvDemucs, filepath.Join(t.TempDir(), "missing"))
	songPath, p := song(t)
	writeStems(t, Dir(songPath, cfg), p, 0.02)

	o, err := Open(songPath, rate, cfg, Auto, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := o.Stems
	if st == nil || !st.Shifted || len(o.Warnings) != 1 {
		t.Fatalf("stems %+v, warnings %v", st, o.Warnings)
	}
	if math.Abs(st.OffsetSec-0.02) > 0.001 {
		t.Errorf("measured offset %.2f ms, want 20", st.OffsetSec*1000)
	}
	lag, _ := audio.MeasureOffset(p["drums"], st.Drums.Samples, rate, 0, 0.1)
	if math.Abs(lag) > 0.001 {
		t.Errorf("drums still %.2f ms off after the shift", lag*1000)
	}
}

func TestOpenFallsBack(t *testing.T) {
	needFFmpeg(t)
	cfg := config.Default().Stems
	t.Setenv(EnvDemucs, filepath.Join(t.TempDir(), "missing"))

	check := func(name string, o *Opened, err error, wantWarning bool) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if o.Stems != nil {
			t.Errorf("%s: stems used", name)
		}
		if o.Mix.Decoder != "wav" {
			t.Errorf("%s: mix decoded with %s, want the native decoder (as without stems)", name, o.Mix.Decoder)
		}
		if (len(o.Warnings) > 0) != wantWarning {
			t.Errorf("%s: warnings %v (note %q)", name, o.Warnings, o.Note)
		}
	}

	songPath, p := song(t)
	o, err := Open(songPath, rate, cfg, Auto, nil)
	check("no cache, no demucs", o, err, false)
	if !strings.Contains(o.Note, "demucs") {
		t.Errorf("note %q", o.Note)
	}

	writeStems(t, Dir(songPath, cfg), p, 0)
	o, err = Open(songPath, rate, cfg, Off, nil)
	check("-no-stems", o, err, false)

	// stale: stems of a shorter song
	short := parts(10)
	writeStems(t, Dir(songPath, cfg), short, 0)
	o, err = Open(songPath, rate, cfg, Auto, nil)
	check("stale (length)", o, err, true)

	// stale: same length, different music (time-reversed)
	rev := map[string][]float32{}
	for name, s := range p {
		r := make([]float32, len(s))
		for i, v := range s {
			r[len(s)-1-i] = v
		}
		rev[name] = r
	}
	writeStems(t, Dir(songPath, cfg), rev, 0)
	o, err = Open(songPath, rate, cfg, Auto, nil)
	check("stale (content)", o, err, true)
}

func TestOpenSeparates(t *testing.T) {
	needFFmpeg(t)
	cfg := config.Default().Stems
	songPath, p := song(t)
	src := t.TempDir()
	writeStems(t, src, p, 0)
	fakeDemucs(t, src)

	o, err := Open(songPath, rate, cfg, Auto, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stems == nil || !o.Stems.Separated || o.Stems.Device != "cpu" || !Cached(songPath, cfg) {
		t.Fatalf("stems %+v, warnings %v", o.Stems, o.Warnings)
	}
	// second run: from the cache
	o, err = Open(songPath, rate, cfg, Auto, nil)
	if err != nil || o.Stems == nil || o.Stems.Separated {
		t.Fatalf("second run: stems %+v, %v", o.Stems, err)
	}
}
