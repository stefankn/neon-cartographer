package audio

import "math"

// alignBlock is the envelope resolution for MeasureOffset (≈1.45 ms at 44.1 kHz).
const alignBlock = 64

// envelope returns the mean absolute amplitude per block.
func envelope(s []float32) []float64 {
	out := make([]float64, len(s)/alignBlock)
	for i := range out {
		var sum float64
		for _, v := range s[i*alignBlock : (i+1)*alignBlock] {
			sum += math.Abs(float64(v))
		}
		out[i] = sum / alignBlock
	}
	return out
}

// MeasureOffset finds the delay of other relative to ref, i.e. the lag d
// for which other(t) ≈ ref(t − d). Both must have the same rate. It searches
// d in expected ± maxDev seconds by cross-correlating amplitude envelopes
// over the loudest 20 s of ref, and returns the lag in seconds (sub-block
// precision) and the normalised correlation (≈1 for a good match).
func MeasureOffset(ref, other []float32, rate int, expected, maxDev float64) (lag, corr float64) {
	a, b := envelope(ref), envelope(other)
	blocksPerSec := float64(rate) / alignBlock
	segLen := min(len(a), int(20*blocksPerSec))
	// pick the loudest window of segLen blocks (coarse 1 s steps)
	start, best := 0, -1.0
	for s := 0; s+segLen <= len(a); s += int(blocksPerSec) {
		var e float64
		for _, v := range a[s : s+segLen] {
			e += v
		}
		if e > best {
			start, best = s, e
		}
	}
	seg := a[start : start+segLen]
	var mean float64
	for _, v := range seg {
		mean += v
	}
	mean /= float64(len(seg))

	center := int(math.Round(expected * blocksPerSec))
	dev := int(math.Ceil(maxDev * blocksPerSec))
	scores := make([]float64, 2*dev+1)
	bestK := -1
	for k := range scores {
		lagBlocks := center - dev + k
		var sab, saa, sbb, bm float64
		n := 0
		for i := range seg {
			j := start + i + lagBlocks
			if j >= 0 && j < len(b) {
				bm += b[j]
				n++
			}
		}
		if n < len(seg)/2 {
			continue
		}
		bm /= float64(n)
		for i, v := range seg {
			j := start + i + lagBlocks
			if j < 0 || j >= len(b) {
				continue
			}
			x, y := v-mean, b[j]-bm
			sab += x * y
			saa += x * x
			sbb += y * y
		}
		if saa > 0 && sbb > 0 {
			scores[k] = sab / math.Sqrt(saa*sbb)
		}
		if bestK < 0 || scores[k] > scores[bestK] {
			bestK = k
		}
	}
	if bestK < 0 {
		return expected, 0
	}
	pos := float64(bestK)
	if bestK > 0 && bestK < len(scores)-1 {
		l, c, r := scores[bestK-1], scores[bestK], scores[bestK+1]
		if den := l - 2*c + r; den != 0 {
			pos += 0.5 * (l - r) / den
		}
	}
	return float64(center-dev)/blocksPerSec + pos/blocksPerSec, scores[bestK]
}
