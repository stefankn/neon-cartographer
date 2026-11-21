package analysis

import (
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"

	"github.com/stefankn/neon-cartographer/internal/config"
)

// Hann returns a periodic Hann window of length n.
func Hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// stft computes magnitude spectra frame by frame. Frame i is centred on
// sample i*hop (the signal is zero-padded by win/2 on both sides), so frame
// time = i*hop/rate.
type stft struct {
	win, hop int
	window   []float64
	fft      *fourier.FFT
	buf      []float64
	coeffs   []complex128
}

func newSTFT(win, hop int) *stft {
	return &stft{
		win: win, hop: hop, window: Hann(win), fft: fourier.NewFFT(win),
		buf: make([]float64, win), coeffs: make([]complex128, win/2+1),
	}
}

func (s *stft) numFrames(n int) int { return n/s.hop + 1 }

// frame writes the magnitude spectrum (win/2+1 bins) of frame i into mag.
func (s *stft) frame(samples []float32, i int, mag []float64) {
	start := i*s.hop - s.win/2
	for j := 0; j < s.win; j++ {
		k := start + j
		if k >= 0 && k < len(samples) {
			s.buf[j] = float64(samples[k]) * s.window[j]
		} else {
			s.buf[j] = 0
		}
	}
	s.fft.Coefficients(s.coeffs, s.buf)
	for k, c := range s.coeffs {
		mag[k] = cmplx.Abs(c)
	}
}

// STFT returns the full magnitude spectrogram [frame][bin]. Meant for short
// signals and tests; Analyze streams frames instead.
func STFT(samples []float32, win, hop int) [][]float64 {
	s := newSTFT(win, hop)
	n := s.numFrames(len(samples))
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, win/2+1)
		s.frame(samples, i, out[i])
	}
	return out
}

// BinFreq returns the centre frequency of FFT bin k.
func BinFreq(k, win, rate int) float64 { return float64(k) * float64(rate) / float64(win) }

// bandFeatures are per-frame features of each band.
type bandFeatures struct {
	frameRate float64
	// energy[b][i]: sum of |X|² over band b's bins in frame i.
	energy [][]float64
	// flux[b][i]: half-wave rectified increase of log(1+γ|X|) summed over
	// band b's bins between frames i-1 and i.
	flux [][]float64
	// tone[i]: flux-weighted mean log2 frequency of the bins in the tone
	// range whose log magnitude rose between frames i-1 and i (0 if none);
	// toneWeight[i] is that flux sum.
	tone, toneWeight []float64
}

// computeBandFeatures streams the STFT once and keeps only band features,
// so memory stays O(frames × bands) instead of a full spectrogram.
func computeBandFeatures(samples []float32, cfg config.AnalysisConfig) bandFeatures {
	s := newSTFT(cfg.WindowSize, cfg.HopSize)
	nFrames := s.numFrames(len(samples))
	nBins := cfg.WindowSize/2 + 1
	nb := len(cfg.Bands)

	// bin ranges per band
	lo := make([]int, nb)
	hi := make([]int, nb)
	for b, band := range cfg.Bands {
		lo[b] = int(math.Ceil(band.LowHz * float64(cfg.WindowSize) / float64(cfg.SampleRate)))
		hi[b] = int(math.Floor(band.HighHz * float64(cfg.WindowSize) / float64(cfg.SampleRate)))
		if lo[b] < 1 {
			lo[b] = 1
		}
		if hi[b] > nBins-1 {
			hi[b] = nBins - 1
		}
	}

	tlo := max(1, int(math.Ceil(cfg.ToneLowHz*float64(cfg.WindowSize)/float64(cfg.SampleRate))))
	thi := min(nBins-1, int(math.Floor(cfg.ToneHighHz*float64(cfg.WindowSize)/float64(cfg.SampleRate))))
	logFreq := make([]float64, nBins)
	for k := 1; k < nBins; k++ {
		logFreq[k] = math.Log2(BinFreq(k, cfg.WindowSize, cfg.SampleRate))
	}

	f := bandFeatures{
		frameRate:  float64(cfg.SampleRate) / float64(cfg.HopSize),
		energy:     make([][]float64, nb),
		flux:       make([][]float64, nb),
		tone:       make([]float64, nFrames),
		toneWeight: make([]float64, nFrames),
	}
	for b := range nb {
		f.energy[b] = make([]float64, nFrames)
		f.flux[b] = make([]float64, nFrames)
	}
	mag := make([]float64, nBins)
	logPrev := make([]float64, nBins)
	logCur := make([]float64, nBins)
	for i := range nFrames {
		s.frame(samples, i, mag)
		for k, m := range mag {
			logCur[k] = math.Log1p(cfg.LogCompression * m)
		}
		for b := range nb {
			var e, fl float64
			for k := lo[b]; k <= hi[b]; k++ {
				e += mag[k] * mag[k]
				if i > 0 {
					if d := logCur[k] - logPrev[k]; d > 0 {
						fl += d
					}
				}
			}
			f.energy[b][i] = e
			f.flux[b][i] = fl
		}
		if i > 0 {
			var w, wf float64
			for k := tlo; k <= thi; k++ {
				if d := logCur[k] - logPrev[k]; d > 0 {
					w += d
					wf += d * logFreq[k]
				}
			}
			if w > 0 {
				f.tone[i], f.toneWeight[i] = wf/w, w
			}
		}
		logPrev, logCur = logCur, logPrev
	}
	return f
}
