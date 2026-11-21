package analysis

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/config"
)

// Onset is a detected note start in one band.
type Onset struct {
	Time     float64 `json:"time"` // seconds from audio start
	Band     int     `json:"band"` // index into Result.Bands
	Strength float64 `json:"strength"`
}

// normalize scales a copy of x so its maximum is 1 (all zeros stay zero).
func normalize(x []float64) []float64 {
	var mx float64
	for _, v := range x {
		mx = math.Max(mx, v)
	}
	out := make([]float64, len(x))
	if mx == 0 {
		return out
	}
	for i, v := range x {
		out[i] = v / mx
	}
	return out
}

// movingMedian returns the median of x over a centred window of ±half.
func movingMedian(x []float64, half int) []float64 {
	out := make([]float64, len(x))
	win := make([]float64, 0, 2*half+1)
	for i := range x {
		lo, hi := max(0, i-half), min(len(x)-1, i+half)
		win = append(win[:0], x[lo:hi+1]...)
		sort.Float64s(win)
		out[i] = win[len(win)/2]
	}
	return out
}

// peak is a picked peak at a (sub-)frame position.
type peak struct {
	frame    float64
	strength float64
}

// pickPeaks finds onsets in a normalised envelope with an adaptive
// (moving-median) threshold, local-maximum test and minimum gap.
func pickPeaks(env []float64, frameRate float64, cfg config.AnalysisConfig) []peak {
	half := int(math.Round(cfg.MedianWindowSec * frameRate))
	med := movingMedian(env, half)
	w := cfg.PeakWindowFrames
	minGap := cfg.MinOnsetGapSec * frameRate
	var peaks []peak
	last := math.Inf(-1)
	for i := 1; i < len(env)-1; i++ {
		v := env[i]
		if v < med[i]*cfg.ThresholdMult+cfg.ThresholdDelta {
			continue
		}
		isMax := true
		for j := max(0, i-w); j <= min(len(env)-1, i+w); j++ {
			// strict on the left so a flat top yields one peak
			if env[j] > v || (j < i && env[j] == v) {
				isMax = false
				break
			}
		}
		if !isMax {
			continue
		}
		// parabolic interpolation for sub-frame position
		a, b, c := env[i-1], v, env[i+1]
		pos := float64(i)
		if den := a - 2*b + c; den != 0 {
			pos += 0.5 * (a - c) / den
		}
		if pos-last < minGap {
			continue
		}
		peaks = append(peaks, peak{frame: pos, strength: v})
		last = pos
	}
	return peaks
}

// detectOnsets runs peak picking on each band's normalised flux.
func detectOnsets(env [][]float64, frameRate float64, cfg config.AnalysisConfig) []Onset {
	var out []Onset
	for b, e := range env {
		for _, p := range pickPeaks(e, frameRate, cfg) {
			out = append(out, Onset{Time: p.frame/frameRate + cfg.OnsetLatencySec, Band: b, Strength: p.strength})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}
