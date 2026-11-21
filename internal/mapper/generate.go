package mapper

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Wall is an obstacle in song beats.
type Wall struct {
	Beat      float64
	SlideType int // 0 = right wall, 1 = left wall (editor slideType)
}

// Options control Generate.
type Options struct {
	Difficulties []string // canonical names
	Seed         int64
	Walls        bool
}

// eventKind is what a selected slot turns into.
type eventKind int

const (
	evSingle eventKind = iota
	evPair
	evSpecial
	evRail
)

type event struct {
	slot  slot
	kind  eventKind
	hand  rules.Hand // evSingle, evRail
	rail  railPlan   // evRail
	order int        // per-hand index within the phrase (filled later)
}

// Generate builds every requested difficulty. All playability rules are
// enforced through package rules; notes that can't satisfy them are moved
// to the nearest valid grid cell or dropped.
func Generate(r *analysis.Result, tl Timeline, o Options, cfg config.MapperConfig) (*Map, error) {
	m := &Map{BPM: tl.BPM}
	for di, name := range o.Difficulties {
		dc, ok := cfg.Difficulty(name)
		if !ok {
			return nil, fmt.Errorf("mapper: no configuration for difficulty %q", name)
		}
		l, err := rules.LimitsFor(cfg, name)
		if err != nil {
			return nil, err
		}
		rng := rand.New(rand.NewSource(o.Seed*1000003 + int64(di)))
		d := generateDifficulty(r, tl, name, dc, l, cfg, rng, o.Walls && dc.WallsAllowed)
		m.Difficulties = append(m.Difficulties, d)
	}
	for _, p := range r.Phrases {
		if p.SectionStart {
			gb := math.Round(r.Grid.BeatAt(p.Start))
			m.Bookmarks = append(m.Bookmarks, Bookmark{Beat: tl.SongBeat(gb), Name: p.Label})
		}
	}
	return m, nil
}

func generateDifficulty(r *analysis.Result, tl Timeline, name string, dc config.DifficultyConfig,
	l rules.Limits, cfg config.MapperConfig, rng *rand.Rand, walls bool) Difficulty {
	sel := selectByBars(buildSlots(r, tl, dc, cfg), r, dc, cfg)
	normalizeTone(sel, cfg)
	markSpecials(sel, r, tl, dc)
	rails := pickRails(r, tl, sel, dc, cfg)
	events := assignHands(sel, rails, dc, cfg)
	plans := planPhrases(r.Phrases, rng)
	notes := place(events, plans, tl, l, cfg, rng)
	d := Difficulty{Name: name}
	if walls {
		d.Walls, notes = placeWalls(r, tl, notes, cfg)
	}
	notes = enforce(notes, l)

	for _, n := range notes {
		mn := Note{Beat: math.Round(n.T/tl.Period()*64) / 64, Type: n.Type, X: n.X, Y: n.Y}
		for _, p := range n.Rail {
			mn.Rail = append(mn.Rail, RailPoint{Beat: math.Round(p.T/tl.Period()*64) / 64, X: p.X, Y: p.Y})
		}
		d.Notes = append(d.Notes, mn)
	}
	return d
}

// assignHands turns selected slots and rails into events in time order.
func assignHands(sel []slot, rails []railPlan, dc config.DifficultyConfig, cfg config.MapperConfig) []event {
	var events []event
	ri := 0
	consumed := map[float64]bool{}
	for _, rp := range rails {
		consumed[rp.start] = true
	}
	activeRail := func(b float64) (rules.Hand, bool) {
		for _, rp := range rails {
			if b > rp.start+1e-9 && b <= rp.end+1e-9 {
				return rp.hand, true
			}
		}
		return 0, false
	}
	bandHand := func(band int) (rules.Hand, bool) {
		switch p := cfg.BandHand[band]; {
		case p < 0:
			return rules.Left, true
		case p > 0:
			return rules.Right, true
		}
		return 0, false
	}
	lastHand, run := rules.Left, 0
	emitSingle := func(s slot, h rules.Hand) {
		if h == lastHand {
			run++
		} else {
			lastHand, run = h, 1
		}
		events = append(events, event{slot: s, kind: evSingle, hand: h})
	}
	for _, s := range sel {
		for ri < len(rails) && rails[ri].start <= s.grid+1e-9 {
			rp := rails[ri]
			head := s
			if math.Abs(rp.start-s.grid) > 1e-9 {
				head = slot{grid: rp.start, phrase: s.phrase, label: s.label, loud: s.loud, toneN: -1, repeatOf: -1}
			}
			events = append(events, event{slot: head, kind: evRail, hand: rp.hand, rail: rp})
			lastHand, run = rp.hand, 1
			ri++
		}
		if consumed[s.grid] {
			continue
		}
		if s.special {
			events = append(events, event{slot: s, kind: evSpecial})
			run = 0
			continue
		}
		if rh, busy := activeRail(s.grid); busy {
			events = append(events, event{slot: s, kind: evSingle, hand: 1 - rh})
			continue // runs don't count while the other hand rides a rail
		}
		if dc.Pairs && s.str[0] >= cfg.PairStrength && s.str[2] >= cfg.PairStrength {
			events = append(events, event{slot: s, kind: evPair})
			run = 0
			continue
		}
		low, high := s.str[0], s.str[2]
		var h rules.Hand
		var ok bool
		switch {
		case low > 0 && low >= cfg.BandDominance*high:
			h, ok = bandHand(0)
		case high > 0 && high >= cfg.BandDominance*low:
			h, ok = bandHand(2)
		default:
			h, ok = bandHand(1)
		}
		if !ok {
			h = 1 - lastHand
		}
		if h == lastHand && run >= dc.MaxSameHandRun {
			h = 1 - h
		}
		emitSingle(s, h)
	}
	for ; ri < len(rails); ri++ {
		rp := rails[ri]
		events = append(events, event{slot: slot{grid: rp.start, phrase: -1, toneN: -1, repeatOf: -1}, kind: evRail, hand: rp.hand, rail: rp})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].slot.grid < events[j].slot.grid })
	return events
}

// place turns events into notes at pattern positions, moving each note to
// the nearest grid cell that satisfies the rules (or dropping it).
func place(events []event, plans []phrasePlan, tl Timeline, l rules.Limits, cfg config.MapperConfig, rng *rand.Rand) []rules.Note {
	g := cfg.Geometry
	// per phrase and hand: how many notes, to spread the pattern over them
	type key struct {
		phrase int
		hand   rules.Hand
	}
	total := map[key]int{}
	for _, e := range events {
		for _, h := range eventHands(e) {
			total[key{e.slot.phrase, h}]++
		}
	}
	seen := map[key]int{}
	nextK := func(e event, h rules.Hand) (int, int) {
		k := key{e.slot.phrase, h}
		i := seen[k]
		seen[k]++
		return i, total[k]
	}
	// placed positions per (bar, position in bar, hand), so a bar that
	// repeats an earlier bar of its phrase (see selectByBars) repeats its
	// note positions
	type spot struct {
		bar, pos int
		hand     rules.Hand
	}
	placedAt := map[spot][2]float64{}
	want := func(e event, h rules.Hand) (float64, float64) {
		k, n := nextK(e, h)
		if e.slot.repeatOf >= 0 {
			if p, ok := placedAt[spot{e.slot.repeatOf, e.slot.pos, h}]; ok {
				return p[0], p[1]
			}
		}
		if e.slot.phrase < 0 || e.slot.phrase >= len(plans) {
			return h.Sign() * g.HomeX, g.HomeY
		}
		return plans[e.slot.phrase].target(k, n, h.Sign(), e.slot, cfg)
	}
	remember := func(e event, h rules.Hand, x, y float64) {
		if e.slot.phrase >= 0 {
			placedAt[spot{e.slot.bar, e.slot.pos, h}] = [2]float64{x, y}
		}
	}

	var notes []rules.Note
	var last [2]*rules.Point
	var activeRail *rules.Note // other-hand distance checks
	var railHand rules.Hand

	fits := func(h rules.Hand, single bool, x, y, t float64) bool {
		if !l.InZone(x, y) || (single && !l.OnSide(h, x)) {
			return false
		}
		if last[h] != nil && !l.Reachable(*last[h], x, y, t) {
			return false
		}
		if activeRail != nil && t > activeRail.T && t < activeRail.End() {
			if h == railHand {
				return false
			}
			if rx, ry, ok := railPosAt(*activeRail, t); ok && math.Hypot(x-rx, y-ry) < l.MinSep-1e-9 {
				return false
			}
		}
		return true
	}
	search := func(x0, y0 float64, ok func(x, y float64) bool) (float64, float64, bool) {
		return nearestCell(x0, y0, cfg.PlaceSearchRadius, ok)
	}
	touch := func(h rules.Hand, p rules.Point) { pp := p; last[h] = &pp }

	for _, e := range events {
		t := tl.SongBeatTime(tl.SongBeat(e.slot.grid))
		switch e.kind {
		case evSingle:
			x0, y0 := want(e, e.hand)
			x, y, ok := search(x0, y0, func(x, y float64) bool { return fits(e.hand, true, x, y, t) })
			if !ok {
				continue
			}
			notes = append(notes, rules.Note{T: t, X: x, Y: y, Type: handType(e.hand)})
			touch(e.hand, rules.Point{T: t, X: x, Y: y})
			remember(e, e.hand, x, y)
		case evSpecial:
			x, y, ok := search(cfg.SpecialX, cfg.SpecialY, func(x, y float64) bool {
				return fits(rules.Left, false, x, y, t) && fits(rules.Right, false, x, y, t)
			})
			if !ok {
				continue
			}
			notes = append(notes, rules.Note{T: t, X: x, Y: y, Type: synthfile.BothHandsSpecial})
			touch(rules.Left, rules.Point{T: t, X: x, Y: y})
			touch(rules.Right, rules.Point{T: t, X: x, Y: y})
		case evPair:
			lx0, ly0 := want(e, rules.Left)
			rx0, ry0 := want(e, rules.Right)
			lx, ly, okL := search(lx0, ly0, func(x, y float64) bool { return fits(rules.Left, true, x, y, t) })
			if !okL {
				continue
			}
			rx, ry, okR := search(rx0, ry0, func(x, y float64) bool {
				return fits(rules.Right, true, x, y, t) && x > lx && math.Hypot(x-lx, y-ly) >= l.MinSep-1e-9
			})
			if !okR {
				// fall back to the left note alone
				notes = append(notes, rules.Note{T: t, X: lx, Y: ly, Type: synthfile.LeftHanded})
				touch(rules.Left, rules.Point{T: t, X: lx, Y: ly})
				remember(e, rules.Left, lx, ly)
				continue
			}
			notes = append(notes,
				rules.Note{T: t, X: lx, Y: ly, Type: synthfile.LeftHanded},
				rules.Note{T: t, X: rx, Y: ry, Type: synthfile.RightHanded})
			touch(rules.Left, rules.Point{T: t, X: lx, Y: ly})
			touch(rules.Right, rules.Point{T: t, X: rx, Y: ry})
			remember(e, rules.Left, lx, ly)
			remember(e, rules.Right, rx, ry)
		case evRail:
			x0, y0 := want(e, e.hand)
			x, y, ok := search(x0, y0, func(x, y float64) bool { return fits(e.hand, true, x, y, t) })
			if !ok {
				continue
			}
			endT := tl.SongBeatTime(tl.SongBeat(e.rail.end))
			bend := 1.0
			if rng.Intn(2) == 0 {
				bend = -1
			}
			path := railPath(x, y, e.hand, t, endT, cfg.RailNodeBeats*tl.Period(), bend, l, cfg)
			n := rules.Note{T: t, X: x, Y: y, Type: handType(e.hand), Rail: path}
			notes = append(notes, n)
			rail := n
			activeRail, railHand = &rail, e.hand
			touch(e.hand, path[len(path)-1])
		}
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].T != notes[j].T {
			return notes[i].T < notes[j].T
		}
		return notes[i].Type < notes[j].Type
	})
	return notes
}

func eventHands(e event) []rules.Hand {
	switch e.kind {
	case evPair:
		return []rules.Hand{rules.Left, rules.Right}
	case evSpecial:
		return nil
	}
	return []rules.Hand{e.hand}
}

func handType(h rules.Hand) synthfile.NoteType {
	if h == rules.Left {
		return synthfile.LeftHanded
	}
	return synthfile.RightHanded
}

// railPosAt interpolates a rail's position at time t.
func railPosAt(n rules.Note, t float64) (float64, float64, bool) {
	if t < n.T || t > n.End() {
		return 0, 0, false
	}
	prev := rules.Point{T: n.T, X: n.X, Y: n.Y}
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

// nearestCell returns the grid cell closest to (x0, y0) within radius that
// satisfies ok (ties broken deterministically by scan order).
func nearestCell(x0, y0, radius float64, ok func(x, y float64) bool) (float64, float64, bool) {
	x0, y0 = math.Round(x0), math.Round(y0)
	if ok(x0, y0) {
		return x0, y0, true
	}
	type cell struct{ x, y, d float64 }
	var cells []cell
	r := int(math.Ceil(radius))
	for dx := -r; dx <= r; dx++ {
		for dy := -r; dy <= r; dy++ {
			d := math.Hypot(float64(dx), float64(dy))
			if d > 0 && d <= radius {
				cells = append(cells, cell{x0 + float64(dx), y0 + float64(dy), d})
			}
		}
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].d < cells[j].d })
	for _, c := range cells {
		if ok(c.x, c.y) {
			return c.x, c.y, true
		}
	}
	return 0, 0, false
}

// enforce removes notes until rules.Check reports no violations. Placement
// already respects every rule it can see locally, so this only catches
// interactions (e.g. a dropped note joining two same-hand runs).
func enforce(notes []rules.Note, l rules.Limits) []rules.Note {
	for iter := 0; iter < 50; iter++ {
		vs := rules.Check(rules.Model{Notes: notes}, l)
		if len(vs) == 0 {
			return notes
		}
		drop := map[int]bool{}
		for _, v := range vs {
			drop[v.NoteIdx] = true
		}
		kept := notes[:0:0]
		for i, n := range notes {
			if !drop[i] {
				kept = append(kept, n)
			}
		}
		notes = kept
	}
	return notes
}
