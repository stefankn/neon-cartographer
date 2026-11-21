package mapper

import (
	"math"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// railPlan is a sustain chosen to become a rail.
type railPlan struct {
	start, end float64 // analysis grid beats
	hand       rules.Hand
}

// pickRails chooses sustains to turn into rails: long and loud enough,
// fully playable, at most MaxRailsPerPhrase per phrase, never overlapping
// another rail or a special note.
func pickRails(r *analysis.Result, tl Timeline, sel []slot, dc config.DifficultyConfig, cfg config.MapperConfig) []railPlan {
	if dc.RailMinBeats <= 0 {
		return nil
	}
	type cand struct {
		start, end, level float64
		band              int
	}
	var cands []cand
	for _, s := range r.Sustains {
		if s.LevelDB < cfg.RailMinLevelDB {
			continue
		}
		start, end := s.StartBeat, math.Min(s.EndBeat, s.StartBeat+cfg.RailMaxBeats)
		if dc.Subdivision == 1 { // keep rails on whole beats where notes are
			start, end = math.Ceil(start), math.Floor(end)
		}
		if end-start < dc.RailMinBeats-1e-9 {
			continue
		}
		ok := true
		for b := start; b <= end+1e-9; b += 0.5 {
			if !playable(r, tl, cfg, b) {
				ok = false
				break
			}
		}
		if ok {
			cands = append(cands, cand{start, end, s.LevelDB, s.Band})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].level != cands[j].level {
			return cands[i].level > cands[j].level
		}
		return cands[i].end-cands[i].start > cands[j].end-cands[j].start
	})
	perPhrase := map[int]int{}
	var out []railPlan
	altHand := rules.Left
	for _, c := range cands {
		ph := phraseAt(r.Phrases, r.Grid.TimeOf(c.start)+tl.OffsetSec)
		if ph < 0 || perPhrase[ph] >= dc.MaxRailsPerPhrase {
			continue
		}
		clash := false
		for _, o := range out {
			if c.start <= o.end+0.5 && o.start <= c.end+0.5 {
				clash = true
				break
			}
		}
		for _, s := range sel {
			if s.special && s.grid >= c.start-1e-9 && s.grid <= c.end+1e-9 {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		var h rules.Hand
		switch pref := cfg.BandHand[c.band]; {
		case pref < 0:
			h = rules.Left
		case pref > 0:
			h = rules.Right
		default:
			h, altHand = altHand, 1-altHand
		}
		perPhrase[ph]++
		out = append(out, railPlan{start: c.start, end: c.end, hand: h})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// railPath returns the rail's node positions after the head: one node every
// RailNodeBeats, on a gentle curve whose total movement stays within
// RailSpeedFactor × the hand-speed limit, clamped to the hand's side and
// the comfort zone, each node snapped to the grid. bend (±1) picks the
// direction of the curve.
func railPath(headX, headY float64, hand rules.Hand, startT, endT, nodeSec float64, bend float64,
	l rules.Limits, cfg config.MapperConfig) []rules.Point {
	dur := endT - startT
	maxCells := cfg.RailSpeedFactor * l.MaxHandSpeed * dur / synthfile.GridScale
	reach := math.Min(2.5, maxCells/2)
	ex := -hand.Sign() * reach * 0.6 // drift a little towards the centre
	ey := bend * reach
	var out []rules.Point
	px, py := headX, headY
	prevT := startT
	n := int(math.Max(1, math.Round(dur/nodeSec)))
	for k := 1; k <= n; k++ {
		u := float64(k) / float64(n)
		t := startT + u*dur
		x := math.Round(headX + ex*u + 0.5*reach*math.Sin(math.Pi*u)*hand.Sign())
		y := math.Round(headY + ey*math.Sin(math.Pi*u/2))
		if !l.InZone(x, y) || !l.OnSide(hand, x) || !l.Reachable(rules.Point{T: prevT, X: px, Y: py}, x, y, t) {
			x, y = px, py
		}
		out = append(out, rules.Point{T: t, X: x, Y: y})
		px, py, prevT = x, y, t
	}
	return out
}
