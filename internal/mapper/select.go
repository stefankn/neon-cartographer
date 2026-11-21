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

	// Music features for placement.
	tone     float64 // strength-weighted mean onset tone (log2 Hz), 0 if unknown
	toneN    float64 // tone normalised over the selected slots (0..1), −1 if unknown
	loud     float64 // beat loudness, 0 (≤ QuietDB) … 1 (loudest bar)
	bar, pos int     // bar index and 1/4-beat position in the bar
	repeatOf int     // earlier bar in the same phrase this bar repeats, −1 if none
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
	toneSum, toneW := map[float64]float64{}, map[float64]float64{}
	var order []float64
	for _, o := range src {
		s, ok := byBeat[o.Beat]
		if !ok {
			s = &slot{grid: o.Beat, level: gridLevel(o.Beat), repeatOf: -1}
			byBeat[o.Beat] = s
			order = append(order, o.Beat)
		}
		if o.Band >= 0 && o.Band < 3 {
			s.str[o.Band] = math.Max(s.str[o.Band], o.Strength)
		}
		if o.Tone > 0 {
			toneSum[o.Beat] += o.Strength * o.Tone
			toneW[o.Beat] += o.Strength
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
		s.bar, s.pos = barOf(r.Grid, b)
		s.score = metricWeight(s.pos, r.Grid.BeatsPerBar, cfg)
		if toneW[b] > 0 {
			s.tone = toneSum[b] / toneW[b]
		}
		if i := int(math.Floor(b)); i >= 0 && i < len(r.BeatEnergyDB) {
			s.loud = math.Max(0, math.Min(1, (r.BeatEnergyDB[i]-cfg.QuietDB)/-cfg.QuietDB))
		}
		s.score += s.total()
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].grid < out[j].grid })
	return out
}

// barOf returns the bar index of a grid beat and the 1/4-beat position
// within that bar.
func barOf(g analysis.Grid, b float64) (bar, pos int) {
	rel := b - float64(g.DownbeatIndex)
	bar = int(math.Floor(rel / float64(g.BeatsPerBar)))
	pos = int(math.Round((rel - float64(bar*g.BeatsPerBar)) * 4))
	return bar, pos
}

// metricWeight scores a 1/4-beat position in the bar: beat 1, the middle
// beat (beat 3 in 4/4), other beats, half-beat off-beats, 1/4 positions.
func metricWeight(pos, beatsPerBar int, cfg config.MapperConfig) float64 {
	switch {
	case pos == 0:
		return cfg.DownbeatWeight
	case pos%4 == 0 && beatsPerBar%2 == 0 && pos/4 == beatsPerBar/2:
		return cfg.MidBarWeight
	case pos%4 == 0:
		return cfg.BeatWeight
	case pos%2 == 0:
		return cfg.HalfWeight
	}
	return cfg.QuarterWeight
}

// rhythmBar is one bar's candidate slots keyed by position in the bar.
type rhythmBar struct {
	bar    int
	phrase int // phrase of the bar's first slot
	slots  map[int]slot
	sig    map[int]float64 // total strength per position, if ≥ RhythmMinStrength
}

// similarity returns the cosine similarity of two rhythm signatures
// (0 when either is empty). Small strength changes barely move it, so bars
// with the same rhythm group together even when weak onsets differ.
func similarity(a, b map[int]float64) float64 {
	// sum in position order: map order would make equal similarities
	// differ in the last bit and ties resolve differently on every run
	var dot, na, nb float64
	for _, p := range sortedKeys(a) {
		dot += a[p] * b[p]
		na += a[p] * a[p]
	}
	for _, p := range sortedKeys(b) {
		nb += b[p] * b[p]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// barMeans returns, per position of bar i, the mean onset strength over
// bar i and all bars whose rhythm is similar to it (similarity ≥
// RhythmSimilarity). Bars with nearly the same rhythm see nearly the same
// neighbours, so one loud or quiet onset doesn't change the choice.
func barMeans(bars []rhythmBar, i int, cfg config.MapperConfig) map[int]float64 {
	sum := map[int]float64{}
	n := 0
	for j, o := range bars {
		if j != i && similarity(bars[i].sig, o.sig) < cfg.RhythmSimilarity-1e-9 {
			continue
		}
		n++
		for p, s := range o.slots {
			sum[p] += s.total()
		}
	}
	out := map[int]float64{}
	for p := range bars[i].slots {
		out[p] = sum[p] / float64(n)
	}
	return out
}

func sortedKeys(m map[int]float64) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// rankPositions orders positions by score (metric weight + mean
// strength). With a previous order (from a bar with a similar rhythm) it
// starts from that order and moves a position ahead only when it scores
// more than RhythmTieMargin higher, so near ties resolve the same way in
// every repetition.
func rankPositions(score map[int]float64, prev []int, cfg config.MapperConfig) []int {
	var out []int
	for p := range score {
		out = append(out, p)
	}
	sort.Slice(out, func(a, b int) bool {
		if score[out[a]] != score[out[b]] {
			return score[out[a]] > score[out[b]]
		}
		return out[a] < out[b]
	})
	if prev == nil {
		return out
	}
	at := map[int]int{}
	for i, p := range prev {
		at[p] = i
	}
	sort.SliceStable(out, func(a, b int) bool { // prev order first, the rest by score
		ia, oka := at[out[a]]
		ib, okb := at[out[b]]
		if oka && okb {
			return ia < ib
		}
		return oka && !okb
	})
	for i := 1; i < len(out); i++ { // insertion sort with a margin
		for j := i; j > 0 && score[out[j]] > score[out[j-1]]+cfg.RhythmTieMargin; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// strongThreshold returns the total onset strength at the StrongQuantile
// of all slots (+Inf when the fast-onset exception is off).
func strongThreshold(slots []slot, dc config.DifficultyConfig) float64 {
	if dc.StrongQuantile <= 0 || dc.StrongGapBeats <= 0 || len(slots) == 0 {
		return math.Inf(1)
	}
	totals := make([]float64, len(slots))
	for i, s := range slots {
		totals[i] = s.total()
	}
	sort.Float64s(totals)
	return totals[int(math.Round(dc.StrongQuantile*float64(len(totals)-1)))]
}

// selectByBars picks notes bar by bar: round(Density × beats per bar)
// notes per bar (at least 1), taken in the order of rankPositions, so bars
// with the same rhythm get the same notes and main beats come first. The minimum
// gap and subdivision rules apply as before (strong onsets in intense
// phrases may use the 1/4 grid).
func selectByBars(slots []slot, r *analysis.Result, dc config.DifficultyConfig, cfg config.MapperConfig) []slot {
	g := r.Grid
	var bars []rhythmBar
	index := map[int]int{}
	for _, s := range slots { // slots are sorted by grid
		bar, pos := barOf(g, s.grid)
		i, ok := index[bar]
		if !ok {
			i = len(bars)
			index[bar] = i
			bars = append(bars, rhythmBar{bar: bar, phrase: s.phrase, slots: map[int]slot{}, sig: map[int]float64{}})
		}
		bars[i].slots[pos] = s
		if s.total() >= cfg.RhythmMinStrength {
			bars[i].sig[pos] = s.total()
		}
	}
	var accepted []slot // sorted by grid
	tooClose := func(b, gap float64) bool {
		i := sort.Search(len(accepted), func(i int) bool { return accepted[i].grid >= b })
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(accepted) && math.Abs(accepted[j].grid-b) < gap-1e-9 {
				return true
			}
		}
		return false
	}
	strongAt := strongThreshold(slots, dc)
	ranks := make([][]int, len(bars))
	for i, b := range bars {
		mean := barMeans(bars, i, cfg)
		score := map[int]float64{}
		for p, m := range mean {
			score[p] = metricWeight(p, g.BeatsPerBar, cfg) + m
		}
		// a bar that repeats an earlier one starts from its order
		var prev []int
		bestSim := cfg.RhythmSimilarity - 1e-9
		for j := 0; j < i; j++ {
			if sim := similarity(b.sig, bars[j].sig); sim > bestSim {
				prev, bestSim = ranks[j], sim
			}
		}
		ranks[i] = rankPositions(score, prev, cfg)
		// for placement: the most similar earlier bar of the same phrase,
		// if the sounds also match in tone
		repeatOf, repSim := -1, cfg.RhythmSimilarity-1e-9
		for j := i - 1; j >= 0; j-- {
			if bars[j].phrase != b.phrase {
				break
			}
			if sim := similarity(b.sig, bars[j].sig); sim > repSim && sameTone(b, bars[j], cfg) {
				repeatOf, repSim = bars[j].bar, sim
			}
		}
		var cands []slot
		for _, p := range ranks[i] {
			c := b.slots[p]
			c.strong = mean[p] >= strongAt // averaged, so it holds for every repetition
			c.repeatOf = repeatOf
			cands = append(cands, c)
		}
		k := max(1, int(math.Round(dc.Density[cands[0].label]*float64(g.BeatsPerBar))))
		n := 0
		for _, c := range cands {
			if n >= k {
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
			j := sort.Search(len(accepted), func(j int) bool { return accepted[j].grid >= c.grid })
			accepted = append(accepted, slot{})
			copy(accepted[j+1:], accepted[j:])
			accepted[j] = c
			n++
		}
	}
	return accepted
}

// sameTone reports whether bar a sounds like bar b in pitch: the mean tone
// difference over positions where both have a tone is at most
// RepeatToneOctaves. Per-onset tone noise (≈ 0.2 octave) averages out,
// while a melody or register change shifts the mean.
func sameTone(a, b rhythmBar, cfg config.MapperConfig) bool {
	var sum float64
	n := 0
	for p, s := range a.slots {
		if o, ok := b.slots[p]; ok && s.tone > 0 && o.tone > 0 {
			sum += s.tone - o.tone
			n++
		}
	}
	return n == 0 || math.Abs(sum/float64(n)) <= cfg.RepeatToneOctaves+1e-9
}

// normalizeTone sets toneN for every slot with a tone: its position between
// the ToneLowQuantile and ToneHighQuantile tones of all such slots,
// clamped to 0..1. Slots without a tone get −1.
func normalizeTone(sel []slot, cfg config.MapperConfig) {
	var tones []float64
	for _, s := range sel {
		if s.tone > 0 {
			tones = append(tones, s.tone)
		}
	}
	sort.Float64s(tones)
	var lo, hi float64
	if len(tones) > 0 {
		lo = tones[int(math.Round(cfg.ToneLowQuantile*float64(len(tones)-1)))]
		hi = tones[int(math.Round(cfg.ToneHighQuantile*float64(len(tones)-1)))]
	}
	for i := range sel {
		switch {
		case sel[i].tone <= 0:
			sel[i].toneN = -1
		case hi <= lo:
			sel[i].toneN = 0.5
		default:
			sel[i].toneN = math.Max(0, math.Min(1, (sel[i].tone-lo)/(hi-lo)))
		}
	}
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
