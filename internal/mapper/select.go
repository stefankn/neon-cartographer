package mapper

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/config"
)

// slot is a grid position with onsets that may receive a note.
type slot struct {
	grid    float64    // analysis grid beat
	str     [3]float64 // strongest onset per band (low, mid, high)
	level   int        // 1 = beat, 2 = half, 4 = quarter
	score   float64
	phrase  int // index into analysis.Result.Phrases, −1 if none
	label   string
	strong  bool
	special bool
}

func (s slot) total() float64 { return s.str[0] + s.str[1] + s.str[2] }

// gridLevel returns 1, 2 or 4 for a beat value on the 1/4 grid.
func gridLevel(b float64) int {
	f := b - math.Floor(b)
	switch {
	case math.Abs(f) < 1e-9:
		return 1
	case math.Abs(f-0.5) < 1e-9:
		return 2
	}
	return 4
}

// phraseAt returns the phrase containing music time t (−1 if none).
func phraseAt(phrases []analysis.Phrase, t float64) int {
	i := sort.Search(len(phrases), func(i int) bool { return phrases[i].End > t })
	if i < len(phrases) && phrases[i].Start <= t {
		return i
	}
	return -1
}

// playable reports whether a grid beat may hold a note: after the lead-in
// and the start of the music, before its end, and not in silence.
func playable(r *analysis.Result, tl Timeline, cfg config.MapperConfig, grid float64) bool {
	t := tl.SongBeatTime(tl.SongBeat(grid))
	if t < math.Max(cfg.LeadInSec, tl.ExportTime(r.SoundStart)) || t > tl.ExportTime(r.SoundEnd) {
		return false
	}
	return !inSilence(r.Silence, r.Grid.TimeOf(grid)+tl.OffsetSec)
}

// buildSlots collects candidate slots on the finest grid the difficulty
// may use.
func buildSlots(r *analysis.Result, tl Timeline, dc config.DifficultyConfig, cfg config.MapperConfig) []slot {
	sub := dc.Subdivision
	if dc.StrongGapBeats > 0 {
		sub = max(sub, int(math.Round(1/dc.StrongGapBeats)))
	}
	src := r.Snapped[sub]
	byBeat := map[float64]*slot{}
	var order []float64
	for _, o := range src {
		s, ok := byBeat[o.Beat]
		if !ok {
			s = &slot{grid: o.Beat, level: gridLevel(o.Beat)}
			byBeat[o.Beat] = s
			order = append(order, o.Beat)
		}
		if o.Band >= 0 && o.Band < 3 {
			s.str[o.Band] = math.Max(s.str[o.Band], o.Strength)
		}
	}
	var out []slot
	for _, b := range order {
		s := *byBeat[b]
		if !playable(r, tl, cfg, b) {
			continue
		}
		s.phrase = phraseAt(r.Phrases, r.Grid.TimeOf(b)+tl.OffsetSec)
		if s.phrase < 0 || r.Phrases[s.phrase].Label == analysis.LabelSilent {
			continue
		}
		s.label = r.Phrases[s.phrase].Label
		switch s.level {
		case 1:
			if r.Grid.IsDownbeat(int(b)) {
				s.score = cfg.DownbeatWeight
			} else {
				s.score = cfg.BeatWeight
			}
		case 2:
			s.score = cfg.HalfWeight
		default:
			s.score = cfg.QuarterWeight
		}
		s.score += s.total()
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].grid < out[j].grid })
	if dc.StrongQuantile > 0 && len(out) > 0 {
		totals := make([]float64, len(out))
		for i, s := range out {
			totals[i] = s.total()
		}
		sort.Float64s(totals)
		q := totals[int(math.Round(dc.StrongQuantile*float64(len(totals)-1)))]
		for i := range out {
			out[i].strong = out[i].total() >= q
		}
	}
	return out
}

// selectByDensity picks, per phrase, the best-scoring slots up to the
// difficulty's density target while keeping the minimum gap.
func selectByDensity(slots []slot, r *analysis.Result, dc config.DifficultyConfig) []slot {
	byPhrase := map[int][]slot{}
	for _, s := range slots {
		byPhrase[s.phrase] = append(byPhrase[s.phrase], s)
	}
	var accepted []slot // kept sorted by grid
	tooClose := func(b, gap float64) bool {
		i := sort.Search(len(accepted), func(i int) bool { return accepted[i].grid >= b })
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(accepted) && math.Abs(accepted[j].grid-b) < gap-1e-9 {
				return true
			}
		}
		return false
	}
	period := r.Grid.Period()
	for pi, p := range r.Phrases {
		cands := byPhrase[pi]
		if len(cands) == 0 {
			continue
		}
		beats := (p.End - p.Start) / period
		target := int(math.Round(dc.Density[p.Label] * beats))
		sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
		n := 0
		for _, c := range cands {
			if n >= target {
				break
			}
			fast := dc.StrongGapBeats > 0 && c.strong && c.label == analysis.LabelIntense
			if c.level > dc.Subdivision && !fast {
				continue
			}
			gap := dc.MinGapBeats
			if fast {
				gap = math.Min(gap, dc.StrongGapBeats)
			}
			if tooClose(c.grid, gap) {
				continue
			}
			i := sort.Search(len(accepted), func(i int) bool { return accepted[i].grid >= c.grid })
			accepted = append(accepted, slot{})
			copy(accepted[i+1:], accepted[i:])
			accepted[i] = c
			n++
		}
	}
	return accepted
}

// markSpecials flags slots that become BothHandsSpecial notes: the first
// note of each section start (drop) and, rate-limited, analysis accents.
func markSpecials(sel []slot, r *analysis.Result, tl Timeline, dc config.DifficultyConfig) {
	idx := map[float64]int{}
	for i, s := range sel {
		idx[s.grid] = i
	}
	var specials []float64
	for _, p := range r.Phrases {
		if !p.SectionStart || p.Label == analysis.LabelSilent {
			continue
		}
		start := math.Round(r.Grid.BeatAt(p.Start - tl.OffsetSec))
		for i, s := range sel {
			if s.grid >= start-1e-9 && s.grid <= start+1+1e-9 {
				sel[i].special = true
				specials = append(specials, s.grid)
				break
			}
		}
	}
	if dc.AccentSpecialBars <= 0 {
		return
	}
	minGap := dc.AccentSpecialBars * float64(r.Grid.BeatsPerBar)
	for _, a := range r.Accents {
		i, ok := idx[a.Beat]
		if !ok {
			continue
		}
		far := true
		for _, sp := range specials {
			if math.Abs(sp-a.Beat) < minGap {
				far = false
				break
			}
		}
		if far {
			sel[i].special = true
			specials = append(specials, a.Beat)
		}
	}
}
