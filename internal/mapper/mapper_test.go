package mapper

import (
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

func TestTimelinePutsGridOnWholeBeats(t *testing.T) {
	for _, bpm := range []float64{126, 90.5, 174} {
		for _, first := range []float64{0, 0.001, 0.427, 0.9, 1.7} {
			for _, off := range []float64{0, 0.03, -0.05} {
				for _, shift := range []float64{0, 0.026, -0.01} {
					g := analysis.Grid{BPM: bpm, FirstBeat: first, BeatsPerBar: 4}
					tl := NewTimeline(g, off, shift)
					p := g.Period()
					if tl.PadSec < 0 || tl.PadSec >= p {
						t.Fatalf("pad %v outside [0, %v)", tl.PadSec, p)
					}
					for _, gb := range []float64{0, 1, 17, 300} {
						// where grid beat gb lands in the exported audio (music shifted by offset)
						t0 := tl.ExportTime(g.TimeOf(gb) + off)
						sb := tl.SongBeat(gb)
						if math.Abs(sb-math.Round(sb)) > 1e-9 || math.Abs(t0-tl.SongBeatTime(sb)) > 1e-9 {
							t.Fatalf("bpm %v first %v off %v shift %v: grid beat %v → song beat %v at %v, want %v",
								bpm, first, off, shift, gb, sb, t0, tl.SongBeatTime(sb))
						}
					}
				}
			}
		}
	}
}

func TestCanonicalDifficulties(t *testing.T) {
	got, err := CanonicalDifficulties("normal, HARD,expert,normal")
	if err != nil || !reflect.DeepEqual(got, []string{"Normal", "Hard", "Expert"}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"", "insane", "custom"} {
		if _, err := CanonicalDifficulties(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// fakeResult: 120 BPM, first beat 0.25 s, onsets on every beat for 60 s,
// silence between 30 and 34 s, music from 0.25 to 58 s.
// richResult: 120 BPM (2 s bars), first beat 0.25 s, 12 phrases of 4 bars:
// calm×3, intense×3, silent, calm×2, intense×3. Onsets on every 1/16:
// kick (low 0.9) on beats, hat (high 0.55) on off-beats, mid 0.3 on 16ths,
// strong low+high together on downbeats. Two sustains and accents.
func richResult() *analysis.Result {
	g := analysis.Grid{BPM: 120, FirstBeat: 0.25, BeatsPerBar: 4}
	labels := []string{"calm", "calm", "calm", "intense", "intense", "intense", "silent", "calm", "calm", "intense", "intense", "intense"}
	r := &analysis.Result{Grid: g, Duration: 98, SoundStart: 0.25, SoundEnd: 96.25, Snapped: map[int][]analysis.SnappedOnset{}}
	for i, lab := range labels {
		p := analysis.Phrase{StartBar: i * 4, EndBar: i*4 + 4, Start: g.TimeOf(float64(i * 16)), End: g.TimeOf(float64(i*16 + 16)), Label: lab}
		p.SectionStart = lab != "silent" && (i == 0 || labels[i-1] != lab)
		r.Phrases = append(r.Phrases, p)
		if lab == "silent" {
			r.Silence = append(r.Silence, analysis.Range{Start: p.Start, End: p.End})
		}
	}
	add := func(sub int, b float64, band int, s float64) {
		for _, sb := range []int{1, 2, 4} {
			if sb >= sub {
				r.Snapped[sb] = append(r.Snapped[sb], analysis.SnappedOnset{Beat: b, Time: g.TimeOf(b), Band: band, Strength: s})
			}
		}
	}
	for q := 0; q < 12*16*4; q++ {
		b := float64(q) / 4
		if r.Phrases[int(b)/16].Label == "silent" {
			continue
		}
		switch {
		case q%16 == 0: // downbeat
			add(1, b, 0, 0.95)
			add(1, b, 2, 0.8)
		case q%4 == 0:
			add(1, b, 0, 0.9)
		case q%4 == 2:
			add(2, b, 2, 0.55)
		default:
			add(4, b, 1, 0.3)
		}
		if q%16 == 0 && r.Phrases[int(b)/16].Label == "intense" {
			r.Accents = append(r.Accents, analysis.Accent{Beat: b, Time: g.TimeOf(b), Strength: 0.9})
		}
	}
	r.Sustains = []analysis.Sustain{
		{Band: 1, StartBeat: 18, EndBeat: 22, LevelDB: -3},   // calm phrase 1
		{Band: 0, StartBeat: 130, EndBeat: 133, LevelDB: -2}, // calm phrase 8
	}
	for i := range r.Sustains {
		r.Sustains[i].Start, r.Sustains[i].End = g.TimeOf(r.Sustains[i].StartBeat), g.TimeOf(r.Sustains[i].EndBeat)
	}
	return r
}

var allDiffs = []string{"Easy", "Normal", "Hard", "Expert", "Master"}

func gen(t *testing.T, seed int64, walls bool) (*Map, Timeline, *analysis.Result) {
	t.Helper()
	r := richResult()
	tl := NewTimeline(r.Grid, 0, 0)
	m, err := Generate(r, tl, Options{Difficulties: allDiffs, Seed: seed, Walls: walls}, config.DefaultMapper())
	if err != nil {
		t.Fatal(err)
	}
	return m, tl, r
}

func TestGenerateHasNoViolations(t *testing.T) {
	m, tl, _ := gen(t, 42, true)
	cfg := config.DefaultMapper()
	for _, d := range m.Difficulties {
		l, _ := rules.LimitsFor(cfg, d.Name)
		model := d.Model(tl)
		if vs := rules.Check(model, l); len(vs) > 0 {
			t.Errorf("%s: %d violations, first: %v", d.Name, len(vs), vs[0])
		}
		if len(d.Notes) == 0 {
			t.Errorf("%s: no notes", d.Name)
		}
	}
}

func TestDensityRisesWithDifficulty(t *testing.T) {
	m, _, _ := gen(t, 42, false)
	prev := 0
	for _, d := range m.Difficulties {
		t.Logf("%-6s %d notes", d.Name, len(d.Notes))
		if len(d.Notes) <= prev {
			t.Errorf("%s has %d notes, not more than the easier difficulty (%d)", d.Name, len(d.Notes), prev)
		}
		prev = len(d.Notes)
	}
}

func TestCalmSparserThanIntenseAndNothingInSilence(t *testing.T) {
	m, tl, r := gen(t, 42, false)
	for _, d := range m.Difficulties {
		var calm, intense int
		for _, n := range d.Notes {
			tm := tl.SongBeatTime(n.Beat)
			if tm < config.DefaultMapper().LeadInSec {
				t.Fatalf("%s: note at %.2fs in the lead-in", d.Name, tm)
			}
			music := tm - tl.PadSec
			p := phraseAt(r.Phrases, music)
			if p < 0 {
				t.Fatalf("%s: note at %.2fs outside the song", d.Name, tm)
			}
			switch r.Phrases[p].Label {
			case "silent":
				t.Fatalf("%s: note at %.2fs in silence", d.Name, tm)
			case "calm":
				calm++
			case "intense":
				intense++
			}
		}
		// 4 calm and 6 intense phrases of equal length
		if float64(calm)/4 >= float64(intense)/6 {
			t.Errorf("%s: calm %d vs intense %d notes per phrase not sparser", d.Name, calm, intense)
		}
	}
}

func TestDeterministicAndSeeded(t *testing.T) {
	a, _, _ := gen(t, 1, true)
	b, _, _ := gen(t, 1, true)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed gave different maps")
	}
	c, _, _ := gen(t, 2, true)
	same := true
	for i := range a.Difficulties {
		if !reflect.DeepEqual(a.Difficulties[i].Notes, c.Difficulties[i].Notes) {
			same = false
		}
	}
	if same {
		t.Fatal("different seeds gave identical maps")
	}
}

func TestMotifPlan(t *testing.T) {
	r := richResult()
	plans := planPhrases(r.Phrases, rand.New(rand.NewSource(7)))
	// calm phrases: 0,1,2,7,8 → A, A', B, B', A
	calm := []int{0, 1, 2, 7, 8}
	want := []struct {
		same   int
		mirror bool
	}{{0, false}, {0, true}, {-1, false}, {2, true}, {0, false}}
	for i, ph := range calm {
		p := plans[ph]
		if p.mirror != want[i].mirror {
			t.Errorf("calm #%d mirror=%v", i, p.mirror)
		}
		if want[i].same >= 0 && p.pattern != plans[calm[want[i].same]].pattern {
			t.Errorf("calm #%d should repeat pattern of calm #%d", i, want[i].same)
		}
	}
	if plans[0].pattern == plans[2].pattern {
		t.Error("motif A and B should differ")
	}
}

func TestSpecialsRailsAndSides(t *testing.T) {
	m, tl, r := gen(t, 42, false)
	for _, d := range m.Difficulties {
		var specials, rails int
		for _, n := range d.Notes {
			if n.Type == synthfile.BothHandsSpecial {
				specials++
			}
			if d.Name == "Easy" || d.Name == "Normal" {
				if (n.Type == synthfile.RightHanded && n.X < 0) || (n.Type == synthfile.LeftHanded && n.X > 0) {
					t.Errorf("%s: crossover at beat %v (x=%v)", d.Name, n.Beat, n.X)
				}
			}
			if len(n.Rail) > 0 {
				rails++
				inSustain := false
				for _, s := range r.Sustains {
					if n.Beat >= tl.SongBeat(s.StartBeat)-1e-9 && n.Rail[len(n.Rail)-1].Beat <= tl.SongBeat(s.EndBeat)+1e-9 {
						inSustain = true
					}
				}
				if !inSustain {
					t.Errorf("%s: rail at beat %v is not on a sustain", d.Name, n.Beat)
				}
				for _, p := range n.Rail {
					if p.X != math.Round(p.X) || p.Y != math.Round(p.Y) {
						t.Errorf("%s: rail node off grid %+v", d.Name, p)
					}
				}
			}
		}
		if specials == 0 {
			t.Errorf("%s: no two-hand specials (section starts)", d.Name)
		}
		if rails == 0 {
			t.Errorf("%s: no rails although the song has sustains", d.Name)
		}
		t.Logf("%-6s specials %d rails %d", d.Name, specials, rails)
	}
}

func TestWalls(t *testing.T) {
	m, tl, r := gen(t, 42, false)
	for _, d := range m.Difficulties {
		if len(d.Walls) > 0 {
			t.Fatalf("%s: walls without -walls", d.Name)
		}
	}
	m, tl, r = gen(t, 42, true)
	cfg := config.DefaultMapper()
	for _, d := range m.Difficulties {
		dc, _ := cfg.Difficulty(d.Name)
		if !dc.WallsAllowed && len(d.Walls) > 0 {
			t.Errorf("%s: walls on a difficulty that doesn't allow them", d.Name)
		}
		if dc.WallsAllowed && len(d.Walls) == 0 {
			t.Errorf("%s: no walls", d.Name)
		}
		for _, w := range d.Walls {
			p := phraseAt(r.Phrases, tl.SongBeatTime(w.Beat)-tl.PadSec)
			if p < 0 || r.Phrases[p].Label != "calm" {
				t.Errorf("%s: wall at beat %v not in a calm phrase", d.Name, w.Beat)
			}
			for _, n := range d.Notes {
				end := n.Beat
				if len(n.Rail) > 0 {
					end = n.Rail[len(n.Rail)-1].Beat
				}
				if w.Beat >= n.Beat-cfg.WallClearBeats && w.Beat <= end+cfg.WallClearBeats {
					t.Errorf("%s: wall at beat %v too close to note at %v", d.Name, w.Beat, n.Beat)
				}
			}
		}
	}
}
