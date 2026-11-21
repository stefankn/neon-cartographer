package generate

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// drumLoop writes a 120 BPM kick/hat loop whose first beat is at phase.
func drumLoop(t *testing.T, path string, bpm, phase, seconds float64) {
	t.Helper()
	const rate = 44100
	s := make([]float32, int(seconds*rate))
	rng := rand.New(rand.NewSource(3))
	for k := 0; ; k++ {
		tb := phase + float64(k)*60/bpm
		if tb > seconds-0.5 {
			break
		}
		st := int(tb * rate)
		ph := 0.0
		for i := 0; i < rate/3 && st+i < len(s); i++ {
			x := float64(i) / rate
			ph += 2 * math.Pi * (55 + 70*math.Exp(-x/0.02)) / rate
			s[st+i] += float32(0.7 * math.Sin(ph) * math.Exp(-x/0.12))
		}
		hs := int((tb + 30/bpm) * rate)
		for i := 0; i < rate/12 && hs+i < len(s); i++ {
			s[hs+i] += float32(0.2 * (2*rng.Float64() - 1) * math.Exp(-float64(i)/(0.02*rate)))
		}
	}
	if err := audio.WriteWAV(path, s, rate); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("sox"); err != nil {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no Ogg encoder")
		}
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "loop.wav")
	const bpm, phase = 120.0, 0.31
	drumLoop(t, in, bpm, phase, 30)

	rep, err := Run(Options{In: in, Difficulties: []string{"Easy", "Normal", "Hard", "Expert", "Master"}, Title: "Loop", Artist: "Test", Walls: true}, config.Default())
	if errors.Is(err, audio.ErrNoEncoder) {
		t.Skip("no Ogg encoder")
	}
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rep.Analysis.Tempo.BPM-bpm) > 0.05 {
		t.Fatalf("BPM %.2f", rep.Analysis.Tempo.BPM)
	}
	f, err := synthfile.Read(rep.Out)
	if err != nil {
		t.Fatal(err)
	}
	if p := synthfile.Check(f); len(p) > 0 {
		t.Fatalf("Check: %v", p)
	}
	if rep.NotesPerDiff["Easy"] < 10 || rep.NotesPerDiff["Normal"] <= rep.NotesPerDiff["Easy"] ||
		rep.NotesPerDiff["Expert"] <= rep.NotesPerDiff["Normal"] {
		t.Fatalf("notes %v", rep.NotesPerDiff)
	}
	if len(rep.Validation) != 5 {
		t.Fatalf("validation covers %d difficulties", len(rep.Validation))
	}
	for _, d := range rep.Validation {
		if len(d.Violations) > 0 {
			t.Fatalf("%s: %v", d.Name, d.Violations)
		}
	}

	// The kicks in the exported audio must sit on whole song beats.
	ogg := f.Entry(f.Beatmap.AudioName)
	oggPath := filepath.Join(dir, "x.ogg")
	if err := os.WriteFile(oggPath, ogg.Data, 0o644); err != nil {
		t.Fatal(err)
	}
	src, _ := audio.Decode(in, 44100)
	dec, err := audio.Decode(oggPath, 44100)
	if err != nil {
		t.Fatal(err)
	}
	lag, _ := audio.MeasureOffset(src.Samples, dec.Samples, 44100, rep.Timeline.PadSec, 0.15)
	firstKick := phase + lag
	period := 60 / f.Beatmap.BPM
	off := math.Mod(firstKick, period)
	off = math.Min(off, period-off)
	if off > 0.003 {
		t.Fatalf("kicks are %.1f ms off the song-beat grid (pad %.4f, lag %.4f)", off*1000, rep.Timeline.PadSec, lag)
	}
	// Easy uses whole beats only; every tick is an integer (1/4-beat grid)
	for diff, sub := range map[string]float64{"Easy": 64, "Master": 16} {
		for key := range f.Beatmap.Track[diff] {
			tick, err := strconv.ParseFloat(key, 64)
			if err != nil || math.Mod(tick, sub) != 0 {
				t.Fatalf("%s: tick %q not on its grid", diff, key)
			}
		}
	}
}
