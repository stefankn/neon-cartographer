package analysis

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/config"
)

// Sustain is a held tone in one band: a rail candidate.
type Sustain struct {
	Band      int     `json:"band"`
	Start     float64 `json:"start"`     // seconds, snapped to the 1/2-beat grid
	End       float64 `json:"end"`       // seconds, snapped to the 1/2-beat grid
	StartBeat float64 `json:"startBeat"` // grid beat index
	EndBeat   float64 `json:"endBeat"`
	LevelDB   float64 `json:"levelDb"` // mean band level relative to the band's loudest frame
}

// detectSustains finds runs of frames where a band is loud (relative to its
// median among non-silent frames) and steady (low coefficient of variation
// of amplitude over a short window), lasting at least SustainMinBeats.
func detectSustains(energy [][]float64, frameRate float64, g Grid, cfg config.AnalysisConfig) []Sustain {
	var out []Sustain
	half := max(1, int(math.Round(cfg.SustainWindowSec*frameRate/2)))
	minFrames := cfg.SustainMinBeats * g.Period() * frameRate
	for b, e := range energy {
		raw := make([]float64, len(e))
		for i, v := range e {
			raw[i] = math.Sqrt(v)
		}
		amp := movingMedian(raw, max(1, int(math.Round(cfg.SustainMedianSec*frameRate/2))))
		var peak float64
		for _, a := range amp {
			peak = math.Max(peak, a)
		}
		if peak == 0 {
			continue
		}
		// median over frames that are not near-silent (−60 dB below band peak)
		var active []float64
		for _, a := range amp {
			if a > peak*1e-3 {
				active = append(active, a)
			}
		}
		thresh := cfg.SustainMinLevel * quantile(active, 0.5)
		mean := movingMean(amp, half)
		sq := make([]float64, len(amp))
		for i, a := range amp {
			sq[i] = a * a
		}
		meanSq := movingMean(sq, half)

		steady := func(i int) bool {
			if mean[i] < thresh || mean[i] == 0 {
				return false
			}
			variance := math.Max(0, meanSq[i]-mean[i]*mean[i])
			return math.Sqrt(variance)/mean[i] < cfg.SustainMaxCV
		}
		for i := 0; i < len(amp); {
			if !steady(i) {
				i++
				continue
			}
			j := i
			var lvl float64
			for j < len(amp) && steady(j) {
				lvl += amp[j]
				j++
			}
			next := j
			// The CV window needs ~half a window to settle, so extend the
			// run outward while the level stays within 20% of its mean.
			runMean := lvl / float64(j-i)
			near := func(k int) bool { return math.Abs(amp[k]-runMean) <= 0.2*runMean }
			s0, s1 := i, j
			for s0 > 0 && near(s0-1) {
				s0--
			}
			for s1 < len(amp) && near(s1) {
				s1++
			}
			if float64(s1-s0) >= minFrames {
				startBeat, _ := g.Snap(float64(s0)/frameRate+cfg.OnsetLatencySec, 2)
				endBeat, _ := g.Snap(float64(s1)/frameRate+cfg.OnsetLatencySec, 2)
				if endBeat > startBeat {
					out = append(out, Sustain{
						Band: b, StartBeat: startBeat, EndBeat: endBeat,
						Start: g.TimeOf(startBeat), End: g.TimeOf(endBeat),
						LevelDB: toDB(lvl / float64(j-i) / peak),
					})
				}
			}
			i = max(next, s1)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
