package mapper

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
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

// selectedByBar runs the selection for one difficulty and returns the
// selected positions per bar (1/4 beats from the bar start) and each bar's
// section label. Bars cut by the lead-in or the song end are left out.
func selectedByBar(r *analysis.Result, dc config.DifficultyConfig) (map[int][]int, map[int]string) {
	cfg := config.DefaultMapper()
	tl := NewTimeline(r.Grid, 0, 0)
	slots := buildSlots(r, tl, dc, cfg)
	first, last := math.MaxInt, math.MinInt
	for _, s := range slots {
		b, _ := barOf(r.Grid, s.grid)
		first, last = min(first, b), max(last, b)
	}
	pos, label := map[int][]int{}, map[int]string{}
	for _, s := range selectByBars(slots, r, dc, cfg) {
		b, p := barOf(r.Grid, s.grid)
		if b == first || b == last {
			continue
		}
		pos[b] = append(pos[b], p)
		label[b] = s.label
	}
	return pos, label
}

func TestRepeatedBarsGetSameRhythm(t *testing.T) {
	r := richResult()
	// same rhythm in every bar, strengths varied by up to ±20 %
	for sub, os := range r.Snapped {
		for i := range os {
			r.Snapped[sub][i].Strength *= 1 + 0.2*math.Sin(os[i].Beat*7.3)
		}
	}
	cfg := config.DefaultMapper()
	for _, name := range allDiffs {
		dc, _ := cfg.Difficulty(name)
		pos, label := selectedByBar(r, dc)
		want := map[string]string{}
		for b, ps := range pos {
			got := fmt.Sprint(ps)
			w, ok := want[label[b]]
			if !ok {
				want[label[b]] = got
				continue
			}
			if got != w {
				t.Errorf("%s bar %d (%s): rhythm %s, other %s bars %s", name, b, label[b], got, label[b], w)
			}
		}
		t.Logf("%-6s %v", name, want)
	}
}

func TestBarBudgetIsEven(t *testing.T) {
	r := richResult()
	cfg := config.DefaultMapper()
	for _, name := range allDiffs {
		dc, _ := cfg.Difficulty(name)
		pos, label := selectedByBar(r, dc)
		for b, ps := range pos {
			k := max(1, int(math.Round(dc.Density[label[b]]*float64(r.Grid.BeatsPerBar))))
			if len(ps) != k {
				t.Errorf("%s bar %d (%s): %d notes, want %d", name, b, label[b], len(ps), k)
			}
		}
	}
}

func TestMainBeatsFirst(t *testing.T) {
	g := analysis.Grid{BPM: 120, FirstBeat: 0.25, BeatsPerBar: 4}
	r := &analysis.Result{Grid: g, Duration: 40, SoundStart: 0.25, SoundEnd: g.TimeOf(64), Snapped: map[int][]analysis.SnappedOnset{}}
	r.Phrases = []analysis.Phrase{{StartBar: 0, EndBar: 16, Start: g.TimeOf(0), End: g.TimeOf(64), Label: "calm", SectionStart: true}}
	for q := 0; q < 64*2; q++ { // equal onsets on every 1/2 beat
		b := float64(q) / 2
		for _, sub := range []int{2, 4} {
			r.Snapped[sub] = append(r.Snapped[sub], analysis.SnappedOnset{Beat: b, Time: g.TimeOf(b), Band: 1, Strength: 0.5})
		}
	}
	dc := config.DifficultyConfig{Subdivision: 2, MinGapBeats: 0.5, Density: map[string]float64{"calm": 0.5}}
	pos, _ := selectedByBar(r, dc)
	if len(pos) == 0 {
		t.Fatal("no bars selected")
	}
	for b, ps := range pos {
		if fmt.Sprint(ps) != "[0 8]" {
			t.Errorf("bar %d: positions %v, want beats 1 and 3 [0 8]", b, ps)
		}
	}
}

// musicResult is richResult with tones and loudness: every onset's tone
// steps up through four pitches bar by bar (1, 2, 3, 4, 1, …), calm
// phrases are quiet (−15 dB) and intense phrases loud (0 dB).
func musicResult(toneSteps bool) *analysis.Result {
	r := richResult()
	for sub := range r.Snapped {
		for i := range r.Snapped[sub] {
			o := &r.Snapped[sub][i]
			o.Tone = 10
			if toneSteps {
				o.Tone = 8 + float64(int(o.Beat)/4%4)
			}
		}
	}
	for b := 0; b < 12*16; b++ {
		db := -15.0
		if r.Phrases[b/16].Label == "intense" {
			db = 0
		}
		r.BeatEnergyDB = append(r.BeatEnergyDB, db)
	}
	return r
}

func genFrom(t *testing.T, r *analysis.Result) (*Map, Timeline) {
	t.Helper()
	tl := NewTimeline(r.Grid, 0, 0)
	m, err := Generate(r, tl, Options{Difficulties: allDiffs, Seed: 42}, config.DefaultMapper())
	if err != nil {
		t.Fatal(err)
	}
	return m, tl
}

// spearman returns the rank correlation of a and b (ties get the mean rank).
func spearman(a, b []float64) float64 {
	rank := func(v []float64) []float64 {
		idx := make([]int, len(v))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(i, j int) bool { return v[idx[i]] < v[idx[j]] })
		r := make([]float64, len(v))
		for i := 0; i < len(idx); {
			j := i
			for j < len(idx) && v[idx[j]] == v[idx[i]] {
				j++
			}
			for k := i; k < j; k++ {
				r[idx[k]] = float64(i+j-1) / 2
			}
			i = j
		}
		return r
	}
	ra, rb := rank(a), rank(b)
	var ma, mb float64
	for i := range ra {
		ma += ra[i]
		mb += rb[i]
	}
	ma /= float64(len(ra))
	mb /= float64(len(rb))
	var c, va, vb float64
	for i := range ra {
		c += (ra[i] - ma) * (rb[i] - mb)
		va += (ra[i] - ma) * (ra[i] - ma)
		vb += (rb[i] - mb) * (rb[i] - mb)
	}
	return c / math.Sqrt(va*vb)
}

func TestHeightFollowsTone(t *testing.T) {
	r := musicResult(true)
	m, tl := genFrom(t, r)
	for _, d := range m.Difficulties {
		for _, typ := range []synthfile.NoteType{synthfile.LeftHanded, synthfile.RightHanded} {
			var tone, y []float64
			for _, n := range d.Notes {
				if n.Type != typ || len(n.Rail) > 0 {
					continue
				}
				gb := n.Beat - tl.SongBeat(0)
				tone = append(tone, float64(int(gb)/4%4))
				y = append(y, n.Y)
			}
			if c := spearman(tone, y); c < 0.5 {
				t.Errorf("%s %v: tone/height rank correlation %.2f, want ≥ 0.5", d.Name, typ, c)
			}
		}
	}
}

func TestQuietPartsMoveLess(t *testing.T) {
	r := musicResult(true)
	m, tl := genFrom(t, r)
	g := config.DefaultMapper().Geometry
	for _, d := range m.Difficulties {
		sum := map[string]float64{}
		cnt := map[string]float64{}
		for _, n := range d.Notes {
			if n.Type != synthfile.LeftHanded && n.Type != synthfile.RightHanded {
				continue
			}
			p := phraseAt(r.Phrases, tl.SongBeatTime(n.Beat)-tl.PadSec)
			lab := r.Phrases[p].Label
			sum[lab] += math.Hypot(math.Abs(n.X)-g.HomeX, n.Y-g.HomeY)
			cnt[lab]++
		}
		calm, intense := sum["calm"]/cnt["calm"], sum["intense"]/cnt["intense"]
		t.Logf("%-6s mean distance from home: calm %.2f, intense %.2f cells", d.Name, calm, intense)
		if calm >= intense {
			t.Errorf("%s: quiet (calm) notes not closer to home than loud ones: %.2f vs %.2f", d.Name, calm, intense)
		}
	}
}

func TestRepeatedBarsRepeatPositions(t *testing.T) {
	r := musicResult(false) // same rhythm and tone in every bar
	m, tl := genFrom(t, r)
	for _, d := range m.Difficulties {
		type key struct {
			pos int
			typ synthfile.NoteType
		}
		bars := map[int]map[key][2]float64{}
		for _, n := range d.Notes {
			if (n.Type != synthfile.LeftHanded && n.Type != synthfile.RightHanded) || len(n.Rail) > 0 {
				continue
			}
			b, p := barOf(r.Grid, n.Beat-tl.SongBeat(0))
			if bars[b] == nil {
				bars[b] = map[key][2]float64{}
			}
			bars[b][key{p, n.Type}] = [2]float64{n.X, n.Y}
		}
		same, total := 0, 0
		for b, notes := range bars {
			prev, ok := bars[b-1]
			if !ok || (b*4)/16 != ((b-1)*4)/16 { // previous bar in the same phrase only
				continue
			}
			for k, xy := range notes {
				if pxy, ok := prev[k]; ok {
					total++
					if pxy == xy {
						same++
					}
				}
			}
		}
		t.Logf("%-6s %d of %d notes repeat the previous bar's position", d.Name, same, total)
		if total == 0 || float64(same) < 0.9*float64(total) {
			t.Errorf("%s: only %d of %d notes in repeated bars repeat their position", d.Name, same, total)
		}
	}
}

func TestMirrorFlipsSideways(t *testing.T) {
	cfg := config.DefaultMapper()
	s := slot{loud: 1, toneN: 0.5}
	for pi := range patterns {
		for k := range 8 {
			a, _ := phrasePlan{pattern: pi}.target(k, 8, 1, s, cfg)
			b, _ := phrasePlan{pattern: pi, mirror: true}.target(k, 8, 1, s, cfg)
			dx, _ := patterns[pi].at(k, float64(k)/7)
			if math.Abs(dx) >= 1 && (a-cfg.Geometry.HomeX)*(b-cfg.Geometry.HomeX) >= 0 {
				t.Errorf("%s note %d: x %v and mirrored %v on the same side of home", patterns[pi].name, k, a, b)
			}
		}
	}
}
