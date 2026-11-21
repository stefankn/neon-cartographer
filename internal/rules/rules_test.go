package rules

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

func limits(t *testing.T, diff string) Limits {
	t.Helper()
	l, err := LimitsFor(config.DefaultMapper(), diff)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func kinds(vs []Violation) string {
	var k []string
	for _, v := range vs {
		k = append(k, v.Kind)
	}
	return strings.Join(k, ",")
}

func R(t, x, y float64) Note { return Note{T: t, X: x, Y: y, Type: synthfile.RightHanded} }
func L(t, x, y float64) Note { return Note{T: t, X: x, Y: y, Type: synthfile.LeftHanded} }

func TestHandSpeedBoundary(t *testing.T) {
	l := limits(t, "Normal") // 1.5 m/s + 0.10 m slack
	// 1 s apart: allowed 1.6 m ≈ 11.7 cells
	ok := Model{Notes: []Note{R(3, 3, 0), L(3.5, -3, 0), R(4, 3, 0), L(4.5, -3, 0)}}
	if v := Check(ok, l); len(v) != 0 {
		t.Fatalf("unexpected: %v", v)
	}
	// right hand 3 → -? keep within zone/side: move 8 cells (1.09 m) in 0.5 s → needs 2.0 m/s
	fast := Model{Notes: []Note{R(3, -1+1, -3), L(3.25, -4, 0), R(3.5, 6, 4)}}
	if v := Check(fast, l); kinds(v) != KindSpeed {
		t.Fatalf("want one speed violation, got %v", v)
	}
	// same move with 1 s is fine (1.09 m ≤ 1.6 m)
	slow := Model{Notes: []Note{R(3, 0, -3), L(3.5, -4, 0), R(4, 6, 4)}}
	if v := Check(slow, l); len(v) != 0 {
		t.Fatalf("unexpected: %v", v)
	}
}

func TestZoneSideRunLeadIn(t *testing.T) {
	l := limits(t, "Easy") // run ≤ 2, no crossover
	m := Model{Notes: []Note{
		R(1, 3, 0),  // lead-in
		R(3, 3, 6),  // outside zone (y max 4)
		R(5, -2, 0), // crossover on Easy + 3rd right in a row
	}}
	got := kinds(Check(m, l))
	for _, k := range []string{KindLeadIn, KindZone, KindCrossover, KindRun} {
		if !strings.Contains(got, k) {
			t.Errorf("missing %s in %s", k, got)
		}
	}
	if v := Check(Model{Notes: []Note{R(5, -2, 0)}}, limits(t, "Hard")); len(v) != 0 {
		t.Errorf("crossover of 2 cells is allowed on Hard: %v", v)
	}
}

func TestSimultaneous(t *testing.T) {
	l := limits(t, "Hard")
	close := Model{Notes: []Note{L(3, 0, 0), R(3, 1, 0)}}
	if got := kinds(Check(close, l)); got != KindSeparation {
		t.Errorf("close pair: %s", got)
	}
	crossed := Model{Notes: []Note{L(3, 2, 0), R(3, -2, 0)}}
	if got := kinds(Check(crossed, l)); got != KindCrossed {
		t.Errorf("crossed pair: %s", got)
	}
	special := Model{Notes: []Note{{T: 3, Type: synthfile.BothHandsSpecial}, R(3, 4, 0)}}
	if got := kinds(Check(special, l)); !strings.Contains(got, KindSpecial) {
		t.Errorf("special with note: %s", got)
	}
}

func TestOneHandSpecialPicksCheaperHand(t *testing.T) {
	m := Model{Notes: []Note{L(3, -3, 0), R(3.5, 3, 0), {T: 4, X: 4, Y: 0, Type: synthfile.OneHandSpecial}}}
	_, hands := m.Hands()
	if hands[2] != Right {
		t.Fatalf("one-hand special at x=4 went to %v", hands[2])
	}
}

func TestRailConflicts(t *testing.T) {
	l := limits(t, "Hard")
	rail := Note{T: 3, X: 3, Y: 0, Type: synthfile.RightHanded, Rail: []Point{{4, 3, 1}, {5, 3, 0}}}
	busy := Model{Notes: []Note{rail, R(4.5, 4, 0)}}
	if got := kinds(Check(busy, l)); !strings.Contains(got, KindRail) {
		t.Errorf("rail hand used during rail: %s", got)
	}
	near := Model{Notes: []Note{rail, L(4, 2, 1)}}
	if got := kinds(Check(near, l)); !strings.Contains(got, KindRail) {
		t.Errorf("other hand next to rail: %s", got)
	}
	fine := Model{Notes: []Note{rail, L(4, -3, 1), R(5.6, 3, 0)}}
	if v := Check(fine, l); len(v) != 0 {
		t.Errorf("unexpected: %v", v)
	}
}

func TestStatsAndFromSynth(t *testing.T) {
	f, err := synthfile.Read("../../reference/editor-sample.synth")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("editor sample not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	m := FromSynth(f, "Easy")
	s := ComputeStats(m, 0.1)
	if s.Notes != 9 || s.Rails != 4 || s.Specials != 5 || s.Walls != 7 { // 6 slides + 1 crouch
		t.Fatalf("stats %+v", s)
	}
	if m.Notes[0].T != 4.5 || math.Abs(m.Notes[0].X+5) > 1e-3 || m.Notes[0].Type != synthfile.LeftHanded {
		t.Fatalf("first note %+v", m.Notes[0])
	}
	// report only: an editor map need not follow our rules
	t.Logf("editor sample Easy: %d violations", len(Check(m, limits(t, "Easy"))))
}
