// Package analysis extracts tempo, beat grid, onsets, energy sections and
// sustained tones from mono PCM audio.
package analysis

import (
	"errors"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
)

// SnappedOnset is an onset placed on the beat grid.
type SnappedOnset struct {
	Beat     float64 `json:"beat"` // grid beat index (fractional for subdivisions)
	Time     float64 `json:"time"` // grid time (s)
	Band     int     `json:"band"`
	Strength float64 `json:"strength"`
}

// Accent is a strong simultaneous low+high onset: a two-hand special candidate.
type Accent struct {
	Beat     float64 `json:"beat"` // snapped to the 1/2-beat grid
	Time     float64 `json:"time"`
	Strength float64 `json:"strength"` // mean of the two onset strengths
}

// Result is the full analysis of one song.
type Result struct {
	Duration   float64  `json:"duration"`
	SampleRate int      `json:"sampleRate"`
	FrameRate  float64  `json:"frameRate"`
	Bands      []string `json:"bands"`

	Tempo TempoEstimate `json:"tempo"`
	Grid  Grid          `json:"grid"`
	Beats []float64     `json:"beats"` // beat times (s)

	Onsets []Onset `json:"onsets"`
	// Snapped onsets per subdivision (1, 2, 4 per beat); an onset appears
	// for a subdivision only if it is within SnapTolerance of a grid point.
	// One entry per band and grid point (the strongest).
	Snapped map[int][]SnappedOnset `json:"snapped"`

	BeatEnergyDB []float64 `json:"beatEnergyDb"` // RMS per beat, relative to loudest bar
	Bars         []Bar     `json:"bars"`
	Phrases      []Phrase  `json:"phrases"`
	Silence      []Range   `json:"silence"`
	SoundStart   float64   `json:"soundStart"`
	SoundEnd     float64   `json:"soundEnd"`

	Sustains []Sustain `json:"sustains"`
	Accents  []Accent  `json:"accents"`
}

// Analyze runs the full analysis. bpmOverride > 0 fixes the tempo.
func Analyze(a *audio.Audio, cfg config.AnalysisConfig, bpmOverride float64) (*Result, error) {
	if a.Rate != cfg.SampleRate {
		return nil, errors.New("analysis: audio must be resampled to the configured sample rate")
	}
	duration := a.Duration()
	if duration < 5 {
		return nil, errors.New("analysis: audio is shorter than 5 seconds")
	}
	feat := computeBandFeatures(a.Samples, cfg)
	env := make([][]float64, len(feat.flux))
	for b, f := range feat.flux {
		env[b] = normalize(f)
	}

	r := &Result{Duration: duration, SampleRate: a.Rate, FrameRate: feat.frameRate}
	for _, b := range cfg.Bands {
		r.Bands = append(r.Bands, b.Name)
	}

	// Tempo and grid.
	comb := combinedEnvelope(env, cfg.BandWeights, feat.frameRate)
	r.Tempo = estimateTempo(comb, feat.frameRate, cfg, bpmOverride)
	r.Grid = Grid{BPM: r.Tempo.BPM, FirstBeat: r.Tempo.Phase, BeatsPerBar: cfg.BeatsPerBar}
	r.Grid.DownbeatIndex = findDownbeat(env[0], feat.frameRate, r.Grid, duration)
	r.Beats = r.Grid.Beats(duration)

	// Onsets.
	r.Onsets = detectOnsets(env, feat.frameRate, cfg)
	r.Snapped = snapOnsets(r.Onsets, r.Grid, cfg)

	// Energy and sections.
	beatE := beatRMS(a.Samples, a.Rate, r.Beats)
	var loudestBar float64
	r.Bars, loudestBar = computeBars(beatE, r.Beats, r.Grid, duration, cfg)
	r.BeatEnergyDB = make([]float64, len(beatE))
	for i, e := range beatE {
		r.BeatEnergyDB[i] = toDB(e) - loudestBar
	}
	r.Phrases = computePhrases(r.Bars, cfg)
	r.Silence, r.SoundStart, r.SoundEnd = silenceRanges(r.Bars, duration)

	r.Sustains = detectSustains(feat.energy, feat.frameRate, r.Grid, cfg)
	r.Accents = findAccents(r.Onsets, r.Grid, feat.frameRate, cfg)
	return r, nil
}

// snapOnsets places onsets on the grid for each configured subdivision.
func snapOnsets(onsets []Onset, g Grid, cfg config.AnalysisConfig) map[int][]SnappedOnset {
	out := map[int][]SnappedOnset{}
	for _, sub := range cfg.Subdivisions {
		type key struct {
			band int
			beat float64
		}
		best := map[key]SnappedOnset{}
		for _, o := range onsets {
			beat, e := g.Snap(o.Time, sub)
			if e > cfg.SnapTolerance || beat < 0 {
				continue
			}
			k := key{o.Band, beat}
			if cur, ok := best[k]; !ok || o.Strength > cur.Strength {
				best[k] = SnappedOnset{Beat: beat, Time: g.TimeOf(beat), Band: o.Band, Strength: o.Strength}
			}
		}
		list := make([]SnappedOnset, 0, len(best))
		for _, s := range best {
			list = append(list, s)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Beat != list[j].Beat {
				return list[i].Beat < list[j].Beat
			}
			return list[i].Band < list[j].Band
		})
		out[sub] = list
	}
	return out
}

// findAccents pairs low-band and high-band onsets that coincide within two
// hops and are both in the top AccentQuantile of their band's strengths.
func findAccents(onsets []Onset, g Grid, frameRate float64, cfg config.AnalysisConfig) []Accent {
	lowB, highB := 0, len(cfg.Bands)-1
	var lows, highs []Onset
	var ls, hs []float64
	for _, o := range onsets {
		switch o.Band {
		case lowB:
			lows = append(lows, o)
			ls = append(ls, o.Strength)
		case highB:
			highs = append(highs, o)
			hs = append(hs, o.Strength)
		}
	}
	lq, hq := quantile(ls, cfg.AccentQuantile), quantile(hs, cfg.AccentQuantile)
	tol := 2 / frameRate
	var out []Accent
	seen := map[float64]bool{}
	j := 0
	for _, l := range lows {
		if l.Strength < lq {
			continue
		}
		for j < len(highs) && highs[j].Time < l.Time-tol {
			j++
		}
		for k := j; k < len(highs) && highs[k].Time <= l.Time+tol; k++ {
			h := highs[k]
			if h.Strength < hq {
				continue
			}
			beat, e := g.Snap((l.Time+h.Time)/2, 2)
			if e > cfg.SnapTolerance || seen[beat] {
				break
			}
			seen[beat] = true
			out = append(out, Accent{Beat: beat, Time: g.TimeOf(beat), Strength: (l.Strength + h.Strength) / 2})
			break
		}
	}
	return out
}
