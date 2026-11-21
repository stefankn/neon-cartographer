package analysis

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/config"
)

// TempoEstimate is the result of tempo and phase estimation.
type TempoEstimate struct {
	BPM        float64 `json:"bpm"`
	Phase      float64 `json:"phase"`      // time (s) of the first beat, in [0, period)
	Confidence float64 `json:"confidence"` // 0..1, how much the winner stands out
	AltBPM     float64 `json:"altBpm"`     // best candidate not within 4% of BPM or of its octaves
	Overridden bool    `json:"overridden"` // BPM was given by the user
	HalfBeat   bool    `json:"halfBeat"`   // phase moved half a beat by the user (-half-beat)
	// PhaseSource is where the phase comes from: "mix", or "drums" (the
	// drum stem, see Overrides.Drums). MixPhase is the mix's own phase
	// (before -half-beat), kept for comparison.
	PhaseSource string  `json:"phaseSource"`
	MixPhase    float64 `json:"mixPhase"`
	// DrumLevelDB: drum stem RMS relative to the mix (0 without a stem).
	DrumLevelDB float64 `json:"drumLevelDb,omitempty"`
}

// combinedEnvelope is the weighted sum of normalised band envelopes with
// the local mean removed (half-wave rectified), which sharpens periodicity.
func combinedEnvelope(env [][]float64, weights []float64, frameRate float64) []float64 {
	n := len(env[0])
	c := make([]float64, n)
	for b, e := range env {
		w := 1.0
		if b < len(weights) {
			w = weights[b]
		}
		for i, v := range e {
			c[i] += w * v
		}
	}
	half := int(math.Round(0.25 * frameRate))
	mean := movingMean(c, half)
	for i := range c {
		c[i] = math.Max(0, c[i]-mean[i])
	}
	return normalize(c)
}

func movingMean(x []float64, half int) []float64 {
	prefix := make([]float64, len(x)+1)
	for i, v := range x {
		prefix[i+1] = prefix[i] + v
	}
	out := make([]float64, len(x))
	for i := range x {
		lo, hi := max(0, i-half), min(len(x)-1, i+half)
		out[i] = (prefix[hi+1] - prefix[lo]) / float64(hi-lo+1)
	}
	return out
}

// autocorr returns the unbiased autocorrelation of x for lags 0..maxLag.
func autocorr(x []float64, maxLag int) []float64 {
	maxLag = min(maxLag, len(x)-1)
	out := make([]float64, maxLag+1)
	for lag := 0; lag <= maxLag; lag++ {
		var s float64
		for i := 0; i+lag < len(x); i++ {
			s += x[i] * x[i+lag]
		}
		out[lag] = s / float64(len(x)-lag)
	}
	return out
}

// interp linearly interpolates x at fractional index p (0 outside).
func interp(x []float64, p float64) float64 {
	if p < 0 || p > float64(len(x)-1) {
		return 0
	}
	i := int(p)
	if i == len(x)-1 {
		return x[i]
	}
	f := p - float64(i)
	return x[i]*(1-f) + x[i+1]*f
}

// gaussianSmooth blurs x with a Gaussian of the given σ in samples.
func gaussianSmooth(x []float64, sigma float64) []float64 {
	r := int(math.Ceil(3 * sigma))
	k := make([]float64, 2*r+1)
	var ks float64
	for i := range k {
		d := float64(i - r)
		k[i] = math.Exp(-d * d / (2 * sigma * sigma))
		ks += k[i]
	}
	out := make([]float64, len(x))
	for i := range x {
		var s float64
		for j, kv := range k {
			p := i + j - r
			if p >= 0 && p < len(x) {
				s += x[p] * kv
			}
		}
		out[i] = s / ks
	}
	return out
}

// tempoCandidate scores one BPM value from the autocorrelation.
type tempoCandidate struct{ bpm, score float64 }

// coarseTempo scores BPM candidates in steps of 0.5 with a comb over the
// autocorrelation (lag, 2·lag, 4·lag) weighted by a log-Gaussian prior.
func coarseTempo(acf []float64, frameRate float64, cfg config.AnalysisConfig) []tempoCandidate {
	var out []tempoCandidate
	for bpm := cfg.MinBPM; bpm <= cfg.MaxBPM+1e-9; bpm += 0.5 {
		lag := 60 * frameRate / bpm
		s := interp(acf, lag) + 0.5*interp(acf, 2*lag) + 0.25*interp(acf, 4*lag)
		oct := math.Log2(bpm / cfg.PriorCenterBPM)
		prior := math.Exp(-0.5 * oct * oct / (cfg.PriorOctaves * cfg.PriorOctaves))
		out = append(out, tempoCandidate{bpm, s * prior})
	}
	return out
}

// gridScore is the mean of env at the beat times phase + k·period.
func gridScore(env []float64, frameRate, period, phase float64) float64 {
	var s float64
	n := 0
	for t := phase; t*frameRate < float64(len(env)-1); t += period {
		s += interp(env, t*frameRate)
		n++
	}
	if n == 0 {
		return 0
	}
	return s / float64(n)
}

// bestPhase finds the phase in [0, period) maximising gridScore, searched
// in quarter-frame steps.
func bestPhase(env []float64, frameRate, period float64) (phase, score float64) {
	step := 0.25 / frameRate
	for p := 0.0; p < period; p += step {
		if s := gridScore(env, frameRate, period, p); s > score {
			phase, score = p, s
		}
	}
	return phase, score
}

// estimateTempo finds BPM and beat phase. If bpmOverride > 0 only the phase
// is searched.
func estimateTempo(env []float64, frameRate float64, cfg config.AnalysisConfig, bpmOverride float64) TempoEstimate {
	te := estimateTempoRaw(env, frameRate, cfg, bpmOverride)
	period := 60 / te.BPM
	te.Phase = math.Mod(te.Phase+cfg.OnsetLatencySec, period)
	return te
}

func estimateTempoRaw(env []float64, frameRate float64, cfg config.AnalysisConfig, bpmOverride float64) TempoEstimate {
	smooth := gaussianSmooth(env, 1.5)
	if bpmOverride > 0 {
		ph, _ := bestPhase(smooth, frameRate, 60/bpmOverride)
		return TempoEstimate{BPM: bpmOverride, Phase: ph, Confidence: 1, Overridden: true}
	}

	maxLag := int(math.Ceil(4*60*frameRate/cfg.MinBPM)) + 2
	acf := autocorr(env, maxLag)
	cands := coarseTempo(acf, frameRate, cfg)
	sorted := append([]tempoCandidate(nil), cands...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].score > sorted[j].score })
	coarse := sorted[0].bpm

	// Confidence: winner vs the median candidate.
	med := sorted[len(sorted)/2].score
	conf := 0.0
	if sorted[0].score > 0 {
		conf = (sorted[0].score - med) / sorted[0].score
	}
	alt := 0.0
	for _, c := range sorted[1:] {
		if !sameTempoFamily(c.bpm, coarse, 0.04) {
			alt = c.bpm
			break
		}
	}

	// Fine search: BPM and phase together, scored over the whole song.
	lo := coarse * (1 - cfg.FineSearchPct/100)
	hi := coarse * (1 + cfg.FineSearchPct/100)
	best := TempoEstimate{BPM: coarse, Confidence: conf, AltBPM: alt}
	bestScore := -1.0
	for bpm := lo; bpm <= hi+1e-9; bpm += cfg.FineStepBPM {
		ph, s := bestPhase(smooth, frameRate, 60/bpm)
		if s > bestScore {
			bestScore = s
			best.BPM, best.Phase = bpm, ph
		}
	}
	best.BPM = math.Round(best.BPM/cfg.FineStepBPM) * cfg.FineStepBPM
	best.Phase, _ = bestPhase(smooth, frameRate, 60/best.BPM)
	return best
}

// sameTempoFamily reports whether a is within tol of b, b/2, 2b, b·2/3 or b·3/2.
func sameTempoFamily(a, b, tol float64) bool {
	for _, r := range []float64{1, 0.5, 2, 2.0 / 3, 1.5} {
		if math.Abs(a-b*r) <= tol*b*r {
			return true
		}
	}
	return false
}

// findDownbeat returns which beat index (0..beatsPerBar-1) most likely
// starts a bar: the one with the strongest low-band onset envelope.
func findDownbeat(lowEnv []float64, frameRate float64, g Grid, duration float64) int {
	sums := make([]float64, g.BeatsPerBar)
	smooth := gaussianSmooth(lowEnv, 1.5)
	for i, t := range g.Beats(duration) {
		sums[i%g.BeatsPerBar] += interp(smooth, t*frameRate)
	}
	best := 0
	for i, s := range sums {
		if s > sums[best] {
			best = i
		}
	}
	return best
}
