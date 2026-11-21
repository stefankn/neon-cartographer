package analysis

import (
	"math"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/config"
)

const hopSec = 512.0 / testRate // one STFT hop ≈ 11.6 ms

func TestHann(t *testing.T) {
	w := Hann(2048)
	var sum float64
	for _, v := range w {
		sum += v
	}
	if w[0] != 0 || math.Abs(w[1024]-1) > 1e-12 || math.Abs(sum-1024) > 1e-9 {
		t.Fatalf("w[0]=%v w[n/2]=%v sum=%v", w[0], w[1024], sum)
	}
	for i := 1; i < 1024; i++ {
		if math.Abs(w[i]-w[2048-i]) > 1e-12 {
			t.Fatalf("not symmetric at %d", i)
		}
	}
}

func TestSTFTSinePeak(t *testing.T) {
	g := newSignal(1)
	g.tone(1000, 0, 1, 0.5)
	spec := STFT(g.s, 2048, 512)
	frame := spec[len(spec)/2]
	best := 0
	for k, m := range frame {
		if m > frame[best] {
			best = k
		}
	}
	want := int(math.Round(1000 * 2048.0 / testRate))
	if best != want {
		t.Fatalf("peak bin %d (%.1f Hz), want %d", best, BinFreq(best, 2048, testRate), want)
	}
}

func bandOnsets(t *testing.T, g *signal) map[int][]float64 {
	t.Helper()
	cfg := config.Default().Analysis
	f := computeBandFeatures(g.s, cfg)
	env := make([][]float64, len(f.flux))
	for b := range f.flux {
		env[b] = normalize(f.flux[b])
	}
	out := map[int][]float64{}
	for _, o := range detectOnsets(env, f.tone, f.toneWeight, f.frameRate, cfg) {
		out[o.Band] = append(out[o.Band], o.Time)
	}
	return out
}

// matchTimes checks every want time has exactly one got time within tol
// and there are no extra got times.
func matchTimes(t *testing.T, label string, got, want []float64, tol float64) {
	t.Helper()
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for i, g := range got {
			if !used[i] && math.Abs(g-w) <= tol {
				used[i], found = true, true
				break
			}
		}
		if !found {
			t.Errorf("%s: onset at %.3fs not found (got %v)", label, w, got)
		}
	}
	for i, u := range used {
		if !u {
			t.Errorf("%s: extra onset at %.3fs", label, got[i])
		}
	}
}

func TestOnsetsClickTrack(t *testing.T) {
	times := []float64{0.5, 1.1, 1.45, 2.3, 2.55, 3.0, 3.8, 4.25, 5.0, 5.12, 6.4}
	g := newSignal(7)
	for _, tm := range times {
		g.click(tm, 0.5)
	}
	got := bandOnsets(t, g)
	// broadband clicks: must show up in every band
	for b := 0; b < 3; b++ {
		matchTimes(t, []string{"low", "mid", "high"}[b], got[b], times, hopSec)
	}
}

func TestOnsetsSeparateBands(t *testing.T) {
	kicks := []float64{0.5, 1.5, 2.5, 3.5, 4.5}
	hats := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	g := newSignal(6)
	for _, k := range kicks {
		g.kick(k, 0.8)
	}
	for _, h := range hats {
		g.hat(h, 0.5)
	}
	got := bandOnsets(t, g)
	matchTimes(t, "low/kicks", got[0], kicks, hopSec)
	matchTimes(t, "high/hats", got[2], hats, hopSec)
}

func TestTempoClickTracks(t *testing.T) {
	for _, tc := range []struct {
		bpm, phase float64
	}{{120, 0.3}, {90, 0.1}, {140, 0.45}, {100, 0.2}, {160, 0.05}} {
		g := newSignal(30)
		beatsOf(tc.bpm, tc.phase, 30, func(_ int, tm float64) { g.click(tm, 0.5) })
		r := analyze(t, g, 0)
		if math.Abs(r.Tempo.BPM-tc.bpm) > 1 {
			t.Errorf("%v BPM click: detected %.2f", tc.bpm, r.Tempo.BPM)
			continue
		}
		if e := phaseError(r.Grid.FirstBeat, tc.phase, 60/tc.bpm); e > hopSec {
			t.Errorf("%v BPM click: phase %.4f want %.4f (err %.1f ms)", tc.bpm, r.Grid.FirstBeat, tc.phase, e*1000)
		}
	}
}

// A kick on every beat and a hi-hat on every off-beat is the classic
// double-tempo trap: it must stay 120, not 240 (out of range) or 60/80.
func TestTempoOffbeatHats(t *testing.T) {
	g := newSignal(30)
	beatsOf(120, 0.2, 30, func(_ int, tm float64) {
		g.kick(tm, 0.8)
		g.hat(tm+0.25, 0.4)
	})
	r := analyze(t, g, 0)
	if math.Abs(r.Tempo.BPM-120) > 1 {
		t.Fatalf("detected %.2f BPM, want 120", r.Tempo.BPM)
	}
	if e := phaseError(r.Grid.FirstBeat, 0.2, 0.5); e > hopSec {
		t.Fatalf("phase %.4f: beat landed on the hats? (err %.1f ms)", r.Grid.FirstBeat, e*1000)
	}
}

// Fast tempos are octave-ambiguous; 174 may be reported as 87 (both map
// to the same grid). Anything else is wrong.
func TestTempoFastIsOctaveEquivalent(t *testing.T) {
	g := newSignal(30)
	beatsOf(174, 0.1, 30, func(_ int, tm float64) { g.click(tm, 0.5) })
	r := analyze(t, g, 0)
	if math.Abs(r.Tempo.BPM-174) > 1 && math.Abs(r.Tempo.BPM-87) > 0.5 {
		t.Fatalf("detected %.2f, want 174 or 87", r.Tempo.BPM)
	}
}

// The fine search must pin a non-round BPM precisely, or a long song drifts.
func TestTempoFinePrecision(t *testing.T) {
	const bpm, phase, dur = 123.45, 0.37, 180.0
	g := newSignal(dur)
	beatsOf(bpm, phase, dur, func(k int, tm float64) {
		g.kick(tm, 0.8)
		if k%2 == 1 {
			g.hat(tm, 0.3)
		}
	})
	r := analyze(t, g, 0)
	if math.Abs(r.Tempo.BPM-bpm) > 0.05 {
		t.Fatalf("detected %.3f BPM, want %.2f ± 0.05", r.Tempo.BPM, bpm)
	}
	// the last beat must still line up (no drift)
	last := phase + math.Floor((dur-1-phase)*bpm/60)*60/bpm
	_, errFrac := r.Grid.Snap(last, 1)
	if errFrac*60/bpm > 2*hopSec {
		t.Fatalf("grid drifted %.1f ms by %.0fs", errFrac*60/bpm*1000, last)
	}
}

func TestBPMOverride(t *testing.T) {
	g := newSignal(20)
	beatsOf(120, 0.3, 20, func(_ int, tm float64) { g.click(tm, 0.5) })
	r := analyze(t, g, 120)
	if !r.Tempo.Overridden || r.Tempo.BPM != 120 {
		t.Fatalf("override not applied: %+v", r.Tempo)
	}
	if e := phaseError(r.Grid.FirstBeat, 0.3, 0.5); e > hopSec {
		t.Fatalf("phase err %.1f ms", e*1000)
	}
}

func TestGridSnap(t *testing.T) {
	g := Grid{BPM: 120, FirstBeat: 0.1, BeatsPerBar: 4}
	cases := []struct {
		t       float64
		sub     int
		beat    float64
		errFrac float64
	}{
		{0.1, 1, 0, 0},
		{0.6, 1, 1, 0},
		{0.35, 2, 0.5, 0},
		{0.35, 1, 0, 0.5}, // exactly between beats rounds away (to 1 or 0: check |err| only)
		{0.225, 4, 0.25, 0},
		{0.15, 4, 0, 0.4},
		{0.7, 2, 1, 0.4}, // beat 1.2 → nearest half-beat 1.0, 0.4 of a half-beat away
	}
	for _, c := range cases {
		beat, e := g.Snap(c.t, c.sub)
		if math.Abs(e-c.errFrac) > 1e-9 || (c.errFrac < 0.5 && math.Abs(beat-c.beat) > 1e-9) {
			t.Errorf("Snap(%v, %d) = %v, %v; want %v, %v", c.t, c.sub, beat, e, c.beat, c.errFrac)
		}
	}
	if math.Abs(g.TimeOf(3.5)-1.85) > 1e-12 {
		t.Errorf("TimeOf(3.5) = %v", g.TimeOf(3.5))
	}
	g.DownbeatIndex = 2
	if !g.IsDownbeat(2) || !g.IsDownbeat(6) || g.IsDownbeat(4) || !g.IsDownbeat(-2) {
		t.Error("IsDownbeat")
	}
}

func TestSnappedOnsetsRespectTolerance(t *testing.T) {
	g := newSignal(20)
	beatsOf(120, 0.25, 20, func(_ int, tm float64) {
		g.kick(tm, 0.8)
		g.hat(tm+0.125, 0.4) // 16th-note position: only fits the 1/4 grid
	})
	r := analyze(t, g, 120)
	count := func(sub, band int) int {
		n := 0
		for _, s := range r.Snapped[sub] {
			if s.Band == band {
				n++
			}
		}
		return n
	}
	if hats4, hats2 := count(4, 2), count(2, 2); hats4 < 30 || hats2 > 2 {
		t.Fatalf("16th hats: %d on 1/4 grid (want ≥30), %d on 1/2 grid (want ~0)", hats4, hats2)
	}
	if kicks1 := count(1, 0); kicks1 < 30 {
		t.Fatalf("kicks on beat grid: %d", kicks1)
	}
}

// 16 quiet bars, 16 loud bars, 4 silent bars at 120 BPM (2 s per bar).
// Downbeats carry a stronger kick so bars line up with the pattern.
func TestEnergySectionsAndSilence(t *testing.T) {
	const bar = 2.0
	g := newSignal(36 * bar)
	beatsOf(120, 0, 32*bar+0.5, func(k int, tm float64) {
		amp := 0.06
		if tm >= 16*bar {
			amp = 0.6
		}
		kickAmp := amp
		if k%4 == 0 {
			kickAmp *= 1.5
		}
		g.kick(tm, kickAmp)
		g.hat(tm+0.25, amp*0.5)
	})
	r := analyze(t, g, 120)
	if r.Grid.DownbeatIndex != 0 {
		t.Errorf("downbeat index %d, want 0", r.Grid.DownbeatIndex)
	}
	var drop *Phrase
	for i := range r.Phrases {
		p := &r.Phrases[i]
		mid := (p.Start + p.End) / 2
		switch {
		case mid < 16*bar && p.Label != LabelCalm:
			t.Errorf("phrase %.0f-%.0fs: %s, want calm", p.Start, p.End, p.Label)
		case mid > 16*bar && mid < 32*bar && p.Label != LabelIntense:
			t.Errorf("phrase %.0f-%.0fs: %s, want intense", p.Start, p.End, p.Label)
		case mid > 32*bar && p.Label != LabelSilent:
			t.Errorf("phrase %.0f-%.0fs: %s, want silent", p.Start, p.End, p.Label)
		}
		if p.SectionStart && p.Start > 1 {
			drop = p
		}
	}
	if drop == nil || math.Abs(drop.Start-16*bar) > 0.01 {
		t.Errorf("drop at %+v, want start %.0fs", drop, 16*bar)
	}
	if math.Abs(r.SoundEnd-32*bar) > bar+0.01 {
		t.Errorf("sound ends at %.1fs, want ~%.0fs", r.SoundEnd, 32*bar)
	}
	if len(r.Silence) == 0 || r.Silence[len(r.Silence)-1].End != r.Duration {
		t.Errorf("trailing silence missing: %+v", r.Silence)
	}
}

func TestSustainDetection(t *testing.T) {
	build := func(withTone bool) *Result {
		g := newSignal(12)
		beatsOf(120, 0, 12, func(_ int, tm float64) { g.click(tm, 0.4) })
		if withTone {
			g.tone(300, 4, 6, 0.3)
		}
		return analyze(t, g, 120)
	}
	r := build(true)
	var found bool
	for _, s := range r.Sustains {
		if s.Band == 1 && math.Abs(s.Start-4) <= 0.25 && math.Abs(s.End-6) <= 0.25 {
			found = true
		}
	}
	if !found {
		t.Errorf("held 300 Hz tone 4–6 s not found as a mid-band sustain: %+v", r.Sustains)
	}
	if r0 := build(false); len(r0.Sustains) != 0 {
		t.Errorf("clicks alone produced sustains: %+v", r0.Sustains)
	}
}

func TestAccents(t *testing.T) {
	g := newSignal(20)
	beatsOf(120, 0, 20, func(k int, tm float64) {
		if k%8 == 0 { // strong kick+hat together every 2 bars
			g.kick(tm, 0.9)
			g.hat(tm, 0.6)
		} else {
			g.kick(tm, 0.3)
			g.hat(tm+0.25, 0.15)
		}
	})
	r := analyze(t, g, 120)
	if len(r.Accents) == 0 {
		t.Fatal("no accents found")
	}
	for _, a := range r.Accents {
		if math.Mod(a.Beat, 8) != 0 {
			t.Errorf("accent at beat %v, want multiples of 8", a.Beat)
		}
	}
}

func TestOnsetToneFollowsPitch(t *testing.T) {
	g := newSignal(12)
	freqs := []float64{200, 400, 800, 1600}
	start := func(note int) float64 { return 1 + float64(note)*1.2 }
	for i, f := range freqs { // each pitch twice
		for rep := range 2 {
			t0 := start(2*i + rep)
			g.tone(f, t0, t0+0.6, 0.5)
		}
	}
	r := analyze(t, g, 120)
	prev := 0.0
	for i, f := range freqs {
		var sum, n float64
		for rep := range 2 {
			t0 := start(2*i + rep)
			for _, o := range r.Onsets {
				if o.Tone > 0 && o.Time >= t0-0.05 && o.Time <= t0+0.1 {
					sum += o.Tone
					n++
				}
			}
		}
		if n == 0 {
			t.Fatalf("%v Hz: no onset with a tone", f)
		}
		got := sum / n
		t.Logf("%5.0f Hz: tone %.2f (log2 f = %.2f) from %d onsets", f, got, math.Log2(f), int(n))
		if math.Abs(got-math.Log2(f)) > 0.75 {
			t.Errorf("%v Hz: tone %.2f, want ≈ %.2f", f, got, math.Log2(f))
		}
		if got <= prev {
			t.Errorf("%v Hz: tone %.2f not above the lower pitch's %.2f", f, got, prev)
		}
		prev = got
	}
}

func TestHalfBeatOverride(t *testing.T) {
	g := newSignal(30)
	beatsOf(120, 0.3, 30, func(_ int, tm float64) { g.kick(tm, 0.8) })
	a := g.audio()
	cfg := config.Default().Analysis
	plain, err := Analyze(a, cfg, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	half, err := Analyze(a, cfg, Overrides{HalfBeat: true})
	if err != nil {
		t.Fatal(err)
	}
	p := plain.Grid.Period()
	if half.Tempo.BPM != plain.Tempo.BPM || !half.Tempo.HalfBeat || plain.Tempo.HalfBeat {
		t.Fatalf("BPM %v/%v, HalfBeat %v/%v", plain.Tempo.BPM, half.Tempo.BPM, plain.Tempo.HalfBeat, half.Tempo.HalfBeat)
	}
	if e := phaseError(half.Grid.FirstBeat, plain.Grid.FirstBeat+p/2, p); e > 1e-9 {
		t.Errorf("first beat %.4f, want %.4f + half a beat", half.Grid.FirstBeat, plain.Grid.FirstBeat)
	}
	if half.Grid.FirstBeat < 0 || half.Grid.FirstBeat >= p {
		t.Errorf("first beat %.4f outside [0, %.4f)", half.Grid.FirstBeat, p)
	}
	// the kicks now sit between the grid's beats: none on the 1/1 grid,
	// all on half-beat positions of the 1/2 grid
	if n := len(half.Snapped[1]); n > 0 {
		t.Errorf("%d onsets still on whole beats", n)
	}
	for _, o := range half.Snapped[2] {
		if f := o.Beat - math.Floor(o.Beat); math.Abs(f-0.5) > 1e-9 {
			t.Errorf("onset at grid beat %v, want a half-beat position", o.Beat)
			break
		}
	}
}

// drumStemSong builds a mix whose loudest hits sit on the off-beats (like a
// bass line pushing the detected beat half a beat early) plus soft kicks on
// the beats, and the drum stem holding only those kicks, scaled by drumGain.
func drumStemSong(drumGain float64) (mix, drums *signal) {
	const bpm, phase, secs = 120.0, 0.3, 30.0
	half := 30 / bpm
	mix, drums = newSignal(secs), newSignal(secs)
	beatsOf(bpm, phase, secs, func(_ int, tm float64) {
		mix.kick(tm, 0.3)
		drums.kick(tm, 0.3*drumGain)
		mix.kick(tm+half, 0.9)
		mix.click(tm+half, 0.5)
	})
	return mix, drums
}

func TestDrumStemPhase(t *testing.T) {
	cfg := config.Default().Analysis
	mix, drums := drumStemSong(1)
	r, err := Analyze(mix.audio(), cfg, Overrides{Drums: drums.audio()})
	if err != nil {
		t.Fatal(err)
	}
	p := r.Grid.Period()
	if r.Tempo.PhaseSource != "drums" {
		t.Fatalf("phase source %q (drum level %.1f dB)", r.Tempo.PhaseSource, r.Tempo.DrumLevelDB)
	}
	// the drum stem puts the beat on the kicks, the mix half a beat later
	if e := phaseError(r.Grid.FirstBeat, 0.3, p); e > 0.02 {
		t.Errorf("first beat %.3f, want the kicks at 0.300 (error %.3f)", r.Grid.FirstBeat, e)
	}
	if e := phaseError(r.Tempo.MixPhase, 0.3+p/2, p); e > 0.02 {
		t.Errorf("mix phase %.3f, want the off-beats at %.3f — test signal doesn't fool the mix", r.Tempo.MixPhase, 0.3+p/2)
	}

	// -half-beat moves the drum phase
	h, err := Analyze(mix.audio(), cfg, Overrides{Drums: drums.audio(), HalfBeat: true})
	if err != nil {
		t.Fatal(err)
	}
	if e := phaseError(h.Grid.FirstBeat, r.Grid.FirstBeat+p/2, p); e > 1e-9 {
		t.Errorf("-half-beat: first beat %.4f, want %.4f + half a beat", h.Grid.FirstBeat, r.Grid.FirstBeat)
	}
}

func TestDrumStemTooQuiet(t *testing.T) {
	cfg := config.Default().Analysis
	mix, drums := drumStemSong(0.005) // ≈ −46 dB below the kicks: leakage only
	r, err := Analyze(mix.audio(), cfg, Overrides{Drums: drums.audio()})
	if err != nil {
		t.Fatal(err)
	}
	if r.Tempo.PhaseSource != "mix" || r.Tempo.Phase != r.Tempo.MixPhase {
		t.Fatalf("phase source %q at drum level %.1f dB, want the mix", r.Tempo.PhaseSource, r.Tempo.DrumLevelDB)
	}
	if r.Tempo.DrumLevelDB >= cfg.DrumStemMinLevelDB {
		t.Fatalf("drum level %.1f dB isn't below the threshold", r.Tempo.DrumLevelDB)
	}
	plain, err := Analyze(mix.audio(), cfg, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Grid != r.Grid || plain.Tempo.PhaseSource != "mix" {
		t.Errorf("grid %+v differs from the analysis without stem %+v", r.Grid, plain.Grid)
	}
}
