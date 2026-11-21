package audio

import "math"

// resampleZeroCrossings is the half-width of the windowed-sinc kernel in
// zero crossings of the (lower) cutoff frequency.
const resampleZeroCrossings = 16

// Resample converts samples from rate `from` to rate `to` with a
// Blackman-windowed sinc interpolator. When downsampling, the cutoff is
// lowered to the new Nyquist frequency to avoid aliasing.
func Resample(in []float32, from, to int) []float32 {
	if from == to || len(in) == 0 {
		return in
	}
	ratio := float64(to) / float64(from)
	cutoff := math.Min(1, ratio) // relative to input Nyquist
	n := int(math.Floor(float64(len(in)) * ratio))
	out := make([]float32, n)
	half := float64(resampleZeroCrossings) / cutoff // half-width in input samples
	for i := range out {
		center := float64(i) / ratio
		lo := int(math.Ceil(center - half))
		hi := int(math.Floor(center + half))
		if lo < 0 {
			lo = 0
		}
		if hi > len(in)-1 {
			hi = len(in) - 1
		}
		var sum, wsum float64
		for j := lo; j <= hi; j++ {
			x := float64(j) - center
			k := cutoff * sinc(cutoff*x) * blackman(x/half)
			sum += float64(in[j]) * k
			wsum += k
		}
		if wsum != 0 {
			out[i] = float32(sum / wsum)
		}
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// blackman is a Blackman window over u ∈ [−1, 1].
func blackman(u float64) float64 {
	if u <= -1 || u >= 1 {
		return 0
	}
	t := (u + 1) / 2 // 0..1
	return 0.42 - 0.5*math.Cos(2*math.Pi*t) + 0.08*math.Cos(4*math.Pi*t)
}
