package mapper

import (
	"math"
	"math/rand"

	"github.com/stefankn/neon-cartographer/internal/analysis"
)

// pattern gives the offset of the k-th note of one hand in a phrase from
// that hand's home position. u = k/(n−1) is the progress through the
// phrase (0..1). dx > 0 means outward (away from the centre line), so the
// same pattern mirrors naturally between the hands. Offsets stay within
// ±2.5 cells so notes remain in the comfort zone.
type pattern struct {
	name string
	at   func(k int, u float64) (dx, dy float64)
}

var patterns = []pattern{
	{"arc", func(k int, u float64) (float64, float64) {
		return 2 * (1 - 2*u), 2.5*math.Sin(math.Pi*u) - 0.5
	}},
	{"zigzag", func(k int, u float64) (float64, float64) {
		dy := 1.5
		if k%2 == 1 {
			dy = -1.5
		}
		return 1.5 * (1 - 2*u), dy
	}},
	{"rise", func(k int, u float64) (float64, float64) {
		return 0.5 - float64(k%2), -2 + 4*u
	}},
	{"circle", func(k int, u float64) (float64, float64) {
		a := 2 * math.Pi * float64(k) / 8
		return 2 * math.Cos(a), 2 * math.Sin(a)
	}},
	{"steps", func(k int, u float64) (float64, float64) {
		return 1.5 * float64(k%3-1), 1.5 * float64((k/3)%3-1)
	}},
	{"diamond", func(k int, u float64) (float64, float64) {
		d := [][2]float64{{0, 2}, {2, 0}, {0, -2}, {-2, 0}}[k%4]
		return d[0], d[1]
	}},
}

// phrasePlan is the pattern used in one phrase.
type phrasePlan struct {
	pattern int
	mirror  bool // flip vertically
}

// planPhrases assigns a pattern to every phrase. Each section label gets a
// motif of two patterns (A, B) chosen at its first appearance; phrases with
// that label then follow A, A′, B, B′, A, … where ′ is the vertical mirror.
// So every 8 bars repeat a shape, and a returning section returns with its
// own motif.
func planPhrases(phrases []analysis.Phrase, rng *rand.Rand) []phrasePlan {
	motifs := map[string][2]int{}
	count := map[string]int{}
	out := make([]phrasePlan, len(phrases))
	for i, p := range phrases {
		m, ok := motifs[p.Label]
		if !ok {
			a := rng.Intn(len(patterns))
			b := (a + 1 + rng.Intn(len(patterns)-1)) % len(patterns)
			m = [2]int{a, b}
			motifs[p.Label] = m
		}
		c := count[p.Label]
		count[p.Label]++
		out[i] = phrasePlan{pattern: m[(c/2)%2], mirror: c%2 == 1}
	}
	return out
}

// position returns the grid position for the k-th of n notes of a hand
// with the given sign (−1 left, +1 right) in a phrase.
func (pp phrasePlan) position(k, n int, sign, homeX, homeY float64) (x, y float64) {
	u := 0.5
	if n > 1 {
		u = float64(k) / float64(n-1)
	}
	dx, dy := patterns[pp.pattern].at(k, u)
	if pp.mirror {
		dy = -dy
	}
	return math.Round(sign * (homeX + dx)), math.Round(homeY + dy)
}
