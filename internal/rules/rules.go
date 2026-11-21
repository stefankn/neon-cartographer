// Package rules models how a player's hands move through a map and checks
// the playability rules. The mapper uses it to place notes and the validate
// command uses it on any .synth file, so both apply exactly the same rules.
package rules

import (
	"fmt"
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Hand is the player's left or right hand.
type Hand int

const (
	Left  Hand = 0
	Right Hand = 1
)

func (h Hand) String() string {
	if h == Left {
		return "left"
	}
	return "right"
}

// Sign is −1 for left, +1 for right.
func (h Hand) Sign() float64 {
	if h == Left {
		return -1
	}
	return 1
}

// simultaneous is the time below which two notes count as "at once".
const simultaneous = 1e-4

// Point is a position (grid cells) at a time (seconds).
type Point struct{ T, X, Y float64 }

// Note is one orb or rail head in seconds and grid cells.
type Note struct {
	T, X, Y float64
	Type    synthfile.NoteType
	Rail    []Point // following rail nodes, nil for a plain note
	Ref     int     // caller's index, reported back in violations
}

// End is the time the note's hand is free again.
func (n Note) End() float64 {
	if len(n.Rail) > 0 {
		return n.Rail[len(n.Rail)-1].T
	}
	return n.T
}

// Wall is an obstacle at a time.
type Wall struct {
	T         float64
	SlideType int
}

// Model is one difficulty of a map.
type Model struct {
	Notes []Note // sorted by T
	Walls []Wall
}

// Limits are the rules for one difficulty.
type Limits struct {
	MaxHandSpeed   float64 // m/s
	ReachSlackM    float64
	XMin, XMax     float64
	YMin, YMax     float64
	CrossoverCells float64
	MinSep         float64 // cells
	MaxSameHandRun int
	LeadInSec      float64
}

// LimitsFor builds Limits from the mapper config for a difficulty name.
func LimitsFor(cfg config.MapperConfig, diff string) (Limits, error) {
	d, ok := cfg.Difficulty(diff)
	if !ok {
		return Limits{}, fmt.Errorf("rules: no configuration for difficulty %q", diff)
	}
	g := cfg.Geometry
	return Limits{
		MaxHandSpeed: d.MaxHandSpeed, ReachSlackM: g.ReachSlackM,
		XMin: g.XMin, XMax: g.XMax, YMin: g.YMin, YMax: g.YMax,
		CrossoverCells: d.CrossoverCells, MinSep: g.MinSimultaneousSep,
		MaxSameHandRun: d.MaxSameHandRun, LeadInSec: cfg.LeadInSec,
	}, nil
}

// DistanceM is the distance between two grid positions in metres.
func DistanceM(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2) * synthfile.GridScale
}

// Reachable reports whether a hand at prev can touch (x, y) at time t.
func (l Limits) Reachable(prev Point, x, y, t float64) bool {
	dt := t - prev.T
	if dt < 0 {
		return false
	}
	return DistanceM(prev.X, prev.Y, x, y) <= l.MaxHandSpeed*dt+l.ReachSlackM+1e-9
}

// InZone reports whether (x, y) is inside the comfort zone.
func (l Limits) InZone(x, y float64) bool {
	return x >= l.XMin-1e-9 && x <= l.XMax+1e-9 && y >= l.YMin-1e-9 && y <= l.YMax+1e-9
}

// OnSide reports whether hand h may touch x (crossover allowance).
func (l Limits) OnSide(h Hand, x float64) bool {
	return h.Sign()*x >= -l.CrossoverCells-1e-9
}

// Touch is one moment a hand touches a position.
type Touch struct {
	Point
	Hand    Hand
	NoteIdx int  // index into Model.Notes
	Single  bool // a one-hand note (not a BothHandsSpecial)
	Rail    bool // rail node (not the head)
}

// Hands resolves which hand touches each note and returns the touches per
// hand in time order. OneHandSpecial goes to the hand that needs to move
// less; BothHandsSpecial is a touch for both hands. It also returns the
// hand chosen for every note (BothHandsSpecial: Right, by convention).
func (m Model) Hands() (touches [2][]Touch, noteHand []Hand) {
	noteHand = make([]Hand, len(m.Notes))
	last := [2]*Point{}
	for i, n := range m.Notes {
		var hs []Hand
		switch n.Type {
		case synthfile.LeftHanded:
			hs = []Hand{Left}
		case synthfile.RightHanded:
			hs = []Hand{Right}
		case synthfile.BothHandsSpecial:
			hs = []Hand{Left, Right}
		default: // OneHandSpecial: cheaper hand (by required speed)
			cost := func(h Hand) float64 {
				p := last[h]
				if p == nil {
					return math.Abs(n.X - h.Sign()*3)
				}
				dt := math.Max(n.T-p.T, 1e-3)
				return DistanceM(p.X, p.Y, n.X, n.Y) / dt
			}
			if cost(Left) < cost(Right) {
				hs = []Hand{Left}
			} else {
				hs = []Hand{Right}
			}
		}
		noteHand[i] = hs[len(hs)-1]
		for _, h := range hs {
			touches[h] = append(touches[h], Touch{Point: Point{n.T, n.X, n.Y}, Hand: h, NoteIdx: i, Single: len(hs) == 1})
			for _, rp := range n.Rail {
				touches[h] = append(touches[h], Touch{Point: rp, Hand: h, NoteIdx: i, Single: len(hs) == 1, Rail: true})
			}
			p := touches[h][len(touches[h])-1].Point
			last[h] = &p
		}
	}
	return touches, noteHand
}

// railPos interpolates a rail note's position at time t (ok=false outside).
func railPos(n Note, t float64) (x, y float64, ok bool) {
	if len(n.Rail) == 0 || t < n.T || t > n.End() {
		return 0, 0, false
	}
	prev := Point{n.T, n.X, n.Y}
	for _, p := range n.Rail {
		if t <= p.T {
			f := 0.0
			if p.T > prev.T {
				f = (t - prev.T) / (p.T - prev.T)
			}
			return prev.X + f*(p.X-prev.X), prev.Y + f*(p.Y-prev.Y), true
		}
		prev = p
	}
	return prev.X, prev.Y, true
}

// Violation is one broken rule.
type Violation struct {
	T       float64 // seconds
	Kind    string
	Msg     string
	NoteIdx int // index into Model.Notes
}

func (v Violation) String() string {
	m, s := int(v.T)/60, v.T-float64(int(v.T)/60*60)
	return fmt.Sprintf("%d:%06.3f %-14s %s", m, s, v.Kind, v.Msg)
}

// Violation kinds.
const (
	KindSpeed      = "hand-speed"
	KindZone       = "comfort-zone"
	KindCrossover  = "crossover"
	KindSeparation = "separation"
	KindCrossed    = "crossed-hands"
	KindRun        = "same-hand-run"
	KindLeadIn     = "lead-in"
	KindRail       = "rail-conflict"
	KindSpecial    = "special-conflict"
)

// Check returns every rule violation, sorted by time.
func Check(m Model, l Limits) []Violation {
	var out []Violation
	add := func(t float64, kind string, idx int, format string, args ...any) {
		out = append(out, Violation{t, kind, fmt.Sprintf(format, args...), idx})
	}
	if len(m.Notes) > 0 && m.Notes[0].T < l.LeadInSec-1e-9 {
		add(m.Notes[0].T, KindLeadIn, 0, "first note at %.2fs, lead-in is %.1fs", m.Notes[0].T, l.LeadInSec)
	}
	touches, noteHand := m.Hands()

	for _, ts := range touches {
		for i, t := range ts {
			if !l.InZone(t.X, t.Y) {
				add(t.T, KindZone, t.NoteIdx, "%s hand at (%.0f, %.0f) is outside the comfort zone", t.Hand, t.X, t.Y)
			}
			if t.Single && !l.OnSide(t.Hand, t.X) {
				add(t.T, KindCrossover, t.NoteIdx, "%s hand reaches to x=%.0f", t.Hand, t.X)
			}
			if i == 0 {
				continue
			}
			p := ts[i-1]
			if !l.Reachable(p.Point, t.X, t.Y, t.T) {
				dt := t.T - p.T
				speed := math.Inf(1)
				if dt > 0 {
					speed = (DistanceM(p.X, p.Y, t.X, t.Y) - l.ReachSlackM) / dt
				}
				add(t.T, KindSpeed, t.NoteIdx, "%s hand needs %.2f m/s (limit %.2f): %.2f m in %.0f ms",
					t.Hand, speed, l.MaxHandSpeed, DistanceM(p.X, p.Y, t.X, t.Y), dt*1000)
			}
		}
	}

	// Simultaneous notes: separation, crossed hands, specials alone.
	for i := 0; i < len(m.Notes); i++ {
		j := i
		for j+1 < len(m.Notes) && m.Notes[j+1].T-m.Notes[i].T < simultaneous {
			j++
		}
		if j > i {
			group := m.Notes[i : j+1]
			for a := range group {
				if group[a].Type == synthfile.BothHandsSpecial {
					add(group[a].T, KindSpecial, i+a, "both-hands special together with another note")
				}
				for b := a + 1; b < len(group); b++ {
					na, nb := group[a], group[b]
					if d := math.Hypot(na.X-nb.X, na.Y-nb.Y); d < l.MinSep-1e-9 {
						add(na.T, KindSeparation, i+b, "simultaneous notes only %.1f cells apart (min %.0f)", d, l.MinSep)
					}
					ha, hb := noteHand[i+a], noteHand[i+b]
					if ha != hb {
						left, right := na, nb
						if ha == Right {
							left, right = nb, na
						}
						if left.X >= right.X {
							add(na.T, KindCrossed, i+b, "left note (x=%.0f) is not left of right note (x=%.0f)", left.X, right.X)
						}
					}
				}
			}
		}
		i = j
	}

	// Same-hand runs (single-hand notes; a both-hands note, a pair, or a
	// note played while the other hand rides a rail resets the run).
	duringOtherRail := func(i int) bool {
		for ri, rn := range m.Notes {
			if len(rn.Rail) > 0 && ri != i && noteHand[ri] != noteHand[i] &&
				m.Notes[i].T > rn.T+simultaneous && m.Notes[i].T < rn.End()-simultaneous {
				return true
			}
		}
		return false
	}
	run, runHand := 0, Hand(-1)
	for i, n := range m.Notes {
		pair := (i > 0 && n.T-m.Notes[i-1].T < simultaneous) || (i+1 < len(m.Notes) && m.Notes[i+1].T-n.T < simultaneous)
		if n.Type == synthfile.BothHandsSpecial || pair || duringOtherRail(i) {
			run, runHand = 0, -1
			continue
		}
		h := noteHand[i]
		if h == runHand {
			run++
		} else {
			run, runHand = 1, h
		}
		if run > l.MaxSameHandRun {
			add(n.T, KindRun, i, "%d %s-hand notes in a row (max %d)", run, h, l.MaxSameHandRun)
		}
	}

	// Rails: the rail hand is busy; the other hand must keep its distance.
	for ri, rn := range m.Notes {
		if len(rn.Rail) == 0 {
			continue
		}
		rh := noteHand[ri]
		for i, n := range m.Notes {
			if i == ri || n.T <= rn.T+simultaneous || n.T >= rn.End()-simultaneous {
				continue
			}
			uses := noteHand[i] == rh || n.Type == synthfile.BothHandsSpecial
			if uses {
				add(n.T, KindRail, i, "%s hand has a note while it is on a rail", rh)
				continue
			}
			if x, y, ok := railPos(rn, n.T); ok && math.Hypot(n.X-x, n.Y-y) < l.MinSep-1e-9 {
				add(n.T, KindRail, i, "note is %.1f cells from the other hand's rail (min %.0f)", math.Hypot(n.X-x, n.Y-y), l.MinSep)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}
