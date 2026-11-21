package analysis

import "math"

// Grid is a constant-tempo beat grid.
type Grid struct {
	BPM           float64 `json:"bpm"`
	FirstBeat     float64 `json:"firstBeat"`     // time (s) of beat 0, ≥ 0
	BeatsPerBar   int     `json:"beatsPerBar"`   // 4
	DownbeatIndex int     `json:"downbeatIndex"` // first beat index that starts a bar (0..BeatsPerBar-1)
}

// Period returns the beat length in seconds.
func (g Grid) Period() float64 { return 60 / g.BPM }

// BeatAt returns the fractional beat index at time t.
func (g Grid) BeatAt(t float64) float64 { return (t - g.FirstBeat) / g.Period() }

// TimeOf returns the time of a (fractional) beat index.
func (g Grid) TimeOf(beat float64) float64 { return g.FirstBeat + beat*g.Period() }

// Snap rounds t to the nearest 1/subdiv beat. It returns the snapped beat
// index and the error as a fraction of the subdivision step (0..0.5).
func (g Grid) Snap(t float64, subdiv int) (beat, errFrac float64) {
	x := g.BeatAt(t) * float64(subdiv)
	r := math.Round(x)
	return r / float64(subdiv), math.Abs(x - r)
}

// IsDownbeat reports whether integer beat index i starts a bar.
func (g Grid) IsDownbeat(i int) bool {
	return ((i-g.DownbeatIndex)%g.BeatsPerBar+g.BeatsPerBar)%g.BeatsPerBar == 0
}

// Beats returns all beat times in [0, duration).
func (g Grid) Beats(duration float64) []float64 {
	var out []float64
	for i := 0; ; i++ {
		t := g.TimeOf(float64(i))
		if t >= duration {
			return out
		}
		out = append(out, t)
	}
}
