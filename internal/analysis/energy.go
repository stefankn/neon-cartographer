package analysis

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/config"
)

// Range is a time span in seconds.
type Range struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Bar is one measure.
type Bar struct {
	Index  int     `json:"index"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	RMSdB  float64 `json:"rmsDb"` // relative to the loudest bar (≤ 0)
	Silent bool    `json:"silent"`
}

// Section labels.
const (
	LabelSilent  = "silent"
	LabelCalm    = "calm"
	LabelNormal  = "normal"
	LabelIntense = "intense"
)

// Phrase is a group of PhraseBars bars with an energy label.
type Phrase struct {
	StartBar     int     `json:"startBar"`
	EndBar       int     `json:"endBar"` // exclusive
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	EnergyDB     float64 `json:"energyDb"` // mean of non-silent bars, relative to loudest bar
	Label        string  `json:"label"`
	SectionStart bool    `json:"sectionStart"` // first sound or energy jump (drop)
}

func rms(s []float32) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(s)))
}

func toDB(x float64) float64 {
	if x <= 1e-12 {
		return -240
	}
	return 20 * math.Log10(x)
}

// beatRMS returns the RMS of the samples between consecutive beats; the
// last beat runs to the end of the audio.
func beatRMS(samples []float32, rate int, beats []float64) []float64 {
	out := make([]float64, len(beats))
	for i, t := range beats {
		end := float64(len(samples)) / float64(rate)
		if i+1 < len(beats) {
			end = beats[i+1]
		}
		a, b := int(t*float64(rate)), min(len(samples), int(end*float64(rate)))
		if a < b {
			out[i] = rms(samples[a:b])
		}
	}
	return out
}

// computeBars groups beats into bars starting at the grid's downbeat.
// Beats before the first downbeat (pickup) are not part of any bar. Bar
// levels are relative to the loudest bar, whose absolute dB is returned.
func computeBars(beatEnergy []float64, beats []float64, g Grid, duration float64, cfg config.AnalysisConfig) ([]Bar, float64) {
	var bars []Bar
	for start := g.DownbeatIndex; start < len(beats); start += g.BeatsPerBar {
		end := min(start+g.BeatsPerBar, len(beats))
		var sum float64
		for i := start; i < end; i++ {
			sum += beatEnergy[i] * beatEnergy[i]
		}
		endT := duration
		if end < len(beats) {
			endT = beats[end]
		}
		bars = append(bars, Bar{
			Index: len(bars), Start: beats[start], End: endT,
			RMSdB: toDB(math.Sqrt(sum / float64(end-start))),
		})
	}
	loudest := math.Inf(-1)
	for _, b := range bars {
		loudest = math.Max(loudest, b.RMSdB)
	}
	for i := range bars {
		bars[i].RMSdB -= loudest
		bars[i].Silent = bars[i].RMSdB < -cfg.SilenceDB
	}
	return bars, loudest
}

// quantile returns the q-quantile (0..1) of x (x is not modified).
func quantile(x []float64, q float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[int(math.Round(q*float64(len(s)-1)))]
}

// computePhrases groups bars into phrases and labels them.
func computePhrases(bars []Bar, cfg config.AnalysisConfig) []Phrase {
	var phrases []Phrase
	var energies []float64
	for start := 0; start < len(bars); start += cfg.PhraseBars {
		end := min(start+cfg.PhraseBars, len(bars))
		p := Phrase{StartBar: start, EndBar: end, Start: bars[start].Start, End: bars[end-1].End, Label: LabelSilent, EnergyDB: -240}
		var lin float64
		n := 0
		for _, b := range bars[start:end] {
			if !b.Silent {
				lin += math.Pow(10, b.RMSdB/20)
				n++
			}
		}
		if n > 0 {
			p.EnergyDB = toDB(lin / float64(n))
			p.Label = ""
			energies = append(energies, p.EnergyDB)
		}
		phrases = append(phrases, p)
	}
	calm := quantile(energies, cfg.CalmQuantile)
	intense := quantile(energies, cfg.IntenseQuantile)
	dropDB := toDB(cfg.DropRatio)
	prevDB := math.Inf(-1)
	for i := range phrases {
		p := &phrases[i]
		if p.Label == LabelSilent {
			prevDB = math.Inf(-1)
			continue
		}
		tol := cfg.LabelToleranceDB
		dynamic := intense-calm > 2*tol || (len(energies) > 0 && quantile(energies, 1)-quantile(energies, 0) > 2*tol)
		switch {
		case dynamic && p.EnergyDB <= calm+tol:
			p.Label = LabelCalm
		case dynamic && p.EnergyDB >= intense-tol:
			p.Label = LabelIntense
		default:
			p.Label = LabelNormal
		}
		p.SectionStart = math.IsInf(prevDB, -1) || p.EnergyDB-prevDB >= dropDB
		prevDB = p.EnergyDB
	}
	return phrases
}

// silenceRanges returns spans of silent bars, plus audio before the first
// and after the last non-silent bar.
func silenceRanges(bars []Bar, duration float64) (ranges []Range, soundStart, soundEnd float64) {
	first, last := -1, -1
	for i, b := range bars {
		if !b.Silent {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return []Range{{0, duration}}, duration, duration
	}
	soundStart, soundEnd = bars[first].Start, bars[last].End
	if soundStart > 0 {
		ranges = append(ranges, Range{0, soundStart})
	}
	for i := first; i <= last; i++ {
		if !bars[i].Silent {
			continue
		}
		j := i
		for j+1 <= last && bars[j+1].Silent {
			j++
		}
		ranges = append(ranges, Range{bars[i].Start, bars[j].End})
		i = j
	}
	if soundEnd < duration {
		ranges = append(ranges, Range{soundEnd, duration})
	}
	return ranges, soundStart, soundEnd
}
