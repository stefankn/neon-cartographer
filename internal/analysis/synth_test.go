package analysis

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
)

// Synthetic test-signal helpers. All randomness is seeded.

const testRate = 44100

type signal struct {
	s   []float32
	rng *rand.Rand
}

func newSignal(seconds float64) *signal {
	return &signal{s: make([]float32, int(seconds*testRate)), rng: rand.New(rand.NewSource(1))}
}

func (g *signal) audio() *audio.Audio {
	return &audio.Audio{Samples: g.s, Rate: testRate, SourceRate: testRate, Channels: 1}
}

func (g *signal) add(i int, v float64) {
	if i >= 0 && i < len(g.s) {
		g.s[i] += float32(v)
	}
}

// click: broadband noise burst, 10 ms decay.
func (g *signal) click(t, amp float64) {
	start := int(t * testRate)
	for i := 0; i < testRate/20; i++ {
		g.add(start+i, amp*(2*g.rng.Float64()-1)*math.Exp(-float64(i)/(0.01*testRate)))
	}
}

// kick: 60 Hz sine with a pitch drop, 120 ms decay (low band only).
func (g *signal) kick(t, amp float64) {
	start := int(t * testRate)
	phase := 0.0
	for i := 0; i < testRate/2; i++ {
		x := float64(i) / testRate
		f := 60 + 60*math.Exp(-x/0.02)
		phase += 2 * math.Pi * f / testRate
		g.add(start+i, amp*math.Sin(phase)*math.Exp(-x/0.12))
	}
}

// hat: high-passed noise (first difference), 25 ms decay (high band).
func (g *signal) hat(t, amp float64) {
	start := int(t * testRate)
	prev := 0.0
	for i := 0; i < testRate/10; i++ {
		n := 2*g.rng.Float64() - 1
		g.add(start+i, amp*0.5*(n-prev)*math.Exp(-float64(i)/(0.025*testRate)))
		prev = n
	}
}

// tone: steady sine from t0 to t1 with 10 ms fades.
func (g *signal) tone(freq, t0, t1, amp float64) {
	a, b := int(t0*testRate), int(t1*testRate)
	fade := 0.01 * testRate
	for i := a; i < b; i++ {
		env := math.Min(1, math.Min(float64(i-a)/fade, float64(b-i)/fade))
		g.add(i, amp*env*math.Sin(2*math.Pi*freq*float64(i)/testRate))
	}
}

// beats calls fn for every beat time phase + k·60/bpm < seconds.
func beatsOf(bpm, phase, seconds float64, fn func(k int, t float64)) {
	for k := 0; ; k++ {
		t := phase + float64(k)*60/bpm
		if t >= seconds-0.5 {
			return
		}
		fn(k, t)
	}
}

func analyze(t *testing.T, g *signal, bpmOverride float64) *Result {
	t.Helper()
	r, err := Analyze(g.audio(), config.Default().Analysis, bpmOverride)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// phaseError is the distance between two beat phases modulo the period.
func phaseError(a, b, period float64) float64 {
	d := math.Mod(math.Abs(a-b), period)
	return math.Min(d, period-d)
}
