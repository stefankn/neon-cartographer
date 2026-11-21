package rules

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Stats summarises one difficulty.
type Stats struct {
	Notes    int // all notes, a rail counts once
	Rails    int
	Specials int // one-hand + both-hands specials
	Walls    int
	FirstT   float64
	LastT    float64
	NPSAvg   float64 // notes per second over the playing span
	NPSPeak  float64 // most notes in any PeakWindowSec window, per second
	// MaxSpeed / P95Speed: maximum and 95th percentile of the hand speed
	// each move needs, in m/s, measured like the rule: the first
	// ReachSlackM of every move is free. Directly comparable to the limit.
	MaxSpeed [2]float64
	P95Speed [2]float64
}

// PeakWindowSec is the window for Stats.NPSPeak.
const PeakWindowSec = 4.0

// ComputeStats summarises a model; slackM is the free reach per move
// (Limits.ReachSlackM).
func ComputeStats(m Model, slackM float64) Stats {
	s := Stats{Notes: len(m.Notes), Walls: len(m.Walls)}
	if len(m.Notes) == 0 {
		return s
	}
	for _, n := range m.Notes {
		if len(n.Rail) > 0 {
			s.Rails++
		}
		if n.Type == synthfile.OneHandSpecial || n.Type == synthfile.BothHandsSpecial {
			s.Specials++
		}
	}
	s.FirstT, s.LastT = m.Notes[0].T, m.Notes[len(m.Notes)-1].End()
	if span := s.LastT - s.FirstT; span > 0 {
		s.NPSAvg = float64(len(m.Notes)) / span
	}
	j := 0
	for i := range m.Notes {
		for m.Notes[i].T-m.Notes[j].T > PeakWindowSec {
			j++
		}
		s.NPSPeak = math.Max(s.NPSPeak, float64(i-j+1)/PeakWindowSec)
	}
	touches, _ := m.Hands()
	for h, ts := range touches {
		var speeds []float64
		for i := 1; i < len(ts); i++ {
			dt := ts[i].T - ts[i-1].T
			if dt <= simultaneous {
				continue
			}
			d := math.Max(0, DistanceM(ts[i-1].X, ts[i-1].Y, ts[i].X, ts[i].Y)-slackM)
			speeds = append(speeds, d/dt)
		}
		if len(speeds) > 0 {
			sort.Float64s(speeds)
			s.MaxSpeed[h] = speeds[len(speeds)-1]
			s.P95Speed[h] = speeds[int(math.Round(0.95*float64(len(speeds)-1)))]
		}
	}
	return s
}

// FromSynth builds the model of one difficulty of a .synth file. Times come
// from each note's z coordinate (seconds × 20), positions are converted to
// grid cells.
func FromSynth(f *synthfile.File, diff string) Model {
	var m Model
	bm := f.Beatmap
	toPoint := func(v synthfile.Vec3) Point {
		x, y := synthfile.MetresToGrid(v[0], v[1])
		return Point{T: v[2] / synthfile.ZPerSecond, X: x, Y: y}
	}
	for _, list := range bm.Track[diff] {
		for _, n := range list {
			p := toPoint(n.Position)
			rn := Note{T: p.T, X: p.X, Y: p.Y, Type: n.Type}
			for _, s := range n.Segments {
				rn.Rail = append(rn.Rail, toPoint(s))
			}
			m.Notes = append(m.Notes, rn)
		}
	}
	sort.SliceStable(m.Notes, func(i, j int) bool {
		if m.Notes[i].T != m.Notes[j].T {
			return m.Notes[i].T < m.Notes[j].T
		}
		return m.Notes[i].Type < m.Notes[j].Type
	})
	for _, list := range []map[string][]synthfile.Wall{bm.Slides, bm.Crouchs, bm.Squares, bm.Triangles} {
		for _, w := range list[diff] {
			st := -1
			if w.SlideType != nil {
				st = *w.SlideType
			}
			m.Walls = append(m.Walls, Wall{T: w.Position[2] / synthfile.ZPerSecond, SlideType: st})
		}
	}
	return m
}
