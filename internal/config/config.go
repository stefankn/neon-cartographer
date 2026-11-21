// Package config holds every tuning parameter of neon-cartographer in one place.
package config

// Config is the root configuration.
type Config struct {
	Analysis AnalysisConfig
	Mapper   MapperConfig
	Export   ExportConfig
	Stems    StemsConfig
}

// StemsConfig tunes the optional stem separation (demucs) and the checks on
// cached stems.
type StemsConfig struct {
	// Model is the demucs model; stems are cached in <song>.stems/<Model>/.
	Model string
	// MaxDurationDiffSec: cached stems whose length differs from the song by
	// more than this are stale (another file under the same name) and ignored.
	MaxDurationDiffSec float64
	// MaxSearchSec: the stems' offset against the mix is searched within ±this.
	MaxSearchSec float64
	// MinAlignCorr: stems whose sum correlates less than this with the mix
	// are ignored (measured: 0.98+ on the test songs).
	MinAlignCorr float64
	// MaxOffsetSec: stems further off than this are shifted by the measured
	// lag (expected ≈ 0: mix and stems are both decoded by ffmpeg).
	MaxOffsetSec float64
}

// ExportConfig tunes the written .synth file.
type ExportConfig struct {
	// OggQuality is the Vorbis quality level (0–10; 6 ≈ 192 kbit/s).
	OggQuality int
	// CoverSize: embedded cover art is scaled to fit size×size pixels.
	CoverSize int
	// MaxAlignResidualSec: if the exported audio is off from the analysed
	// audio by more than this (MP3 decoders differ in start delay), the
	// padding is corrected and the audio re-encoded once.
	MaxAlignResidualSec float64
}

// Band is a frequency range in Hz.
type Band struct {
	Name   string
	LowHz  float64
	HighHz float64
}

// AnalysisConfig tunes audio analysis.
type AnalysisConfig struct {
	// SampleRate is the rate all audio is resampled to before analysis.
	SampleRate int
	// WindowSize and HopSize of the STFT, in samples (2048/512 @ 44.1 kHz =
	// 46 ms window, 11.6 ms hop ≈ 86 frames per second).
	WindowSize int
	HopSize    int

	// Bands used for onset detection, energy and sustain detection.
	// Order matters: index 0 = low, 1 = mid, 2 = high.
	Bands []Band
	// LogCompression is γ in log(1 + γ·|X|) applied before spectral flux.
	// Larger values emphasise quiet partials but also amplify spill-over of
	// a drum into other bands (at γ=100 a kick shows up in the high band at
	// ~15% strength; at γ=10 at ~5%).
	LogCompression float64
	// ToneLowHz/ToneHighHz: frequency range of an onset's tone (the
	// flux-weighted mean log2 frequency of what just started sounding),
	// used to place higher sounds higher.
	ToneLowHz, ToneHighHz float64
	// OnsetLatencySec is added to detected onset and beat times. Frames are
	// centred, so the flux peaks ~1 hop before the true attack (measured
	// ≈ 12 ms on synthetic clicks and kicks).
	OnsetLatencySec float64

	// Peak picking on each band's onset envelope.
	// Threshold(t) = median(envelope in ±MedianWindowSec) * ThresholdMult + ThresholdDelta,
	// where the envelope is normalised to a max of 1. ThresholdDelta also
	// rejects cross-band spill-over (real hits ≥ 0.6, spill ≤ 0.11 at γ=10).
	MedianWindowSec float64
	ThresholdMult   float64
	ThresholdDelta  float64
	// PeakWindowFrames: a peak must be the max within ±this many frames.
	PeakWindowFrames int
	// MinOnsetGapSec: minimum time between two onsets in the same band.
	MinOnsetGapSec float64

	// Tempo search range and prior.
	MinBPM float64
	MaxBPM float64
	// PriorCenterBPM / PriorOctaves: log-Gaussian weighting of tempo
	// candidates to resolve half/double ambiguity (σ in octaves).
	PriorCenterBPM float64
	PriorOctaves   float64
	// FineSearchPct: refine BPM within ±this percent of the coarse estimate,
	// in steps of FineStepBPM. Needed because one BPM must hold for the whole
	// song: 0.1 BPM error drifts ~0.45 beat over 4.5 minutes.
	FineSearchPct float64
	FineStepBPM   float64
	// BandWeights weight each band's envelope in the combined envelope used
	// for tempo/phase (low, mid, high).
	BandWeights []float64

	// SnapTolerance: an onset counts for a grid subdivision only if its
	// distance to the nearest grid point is ≤ this fraction of the step.
	SnapTolerance float64
	// Subdivisions per beat to snap onsets to (1 = quarter notes at 4/4).
	Subdivisions []int

	// BeatsPerBar (time signature numerator; Synth Riders maps are 4/4).
	BeatsPerBar int
	// PhraseBars: phrase length used for section labelling.
	PhraseBars int
	// SilenceDB: a bar whose RMS is this many dB below the song's loudest
	// bar is silent.
	SilenceDB float64
	// CalmQuantile/IntenseQuantile split non-silent phrases by energy.
	CalmQuantile    float64
	IntenseQuantile float64
	// LabelToleranceDB widens the quantile cut-offs so near-equal phrases
	// get the same label. If the whole song spans less than 2× this, every
	// phrase is "normal".
	LabelToleranceDB float64
	// DropRatio: a phrase whose energy is ≥ this times the previous phrase's
	// energy starts a new section (drop).
	DropRatio float64

	// Sustained tones (rail candidates).
	// A frame is "sustained" in a band if the band's amplitude is at least
	// SustainMinLevel × the band's median active amplitude and its
	// coefficient of variation over SustainWindowSec is below SustainMaxCV.
	// Runs must last ≥ SustainMinBeats.
	// SustainMedianSec: band amplitude is first median-filtered over this
	// window so short drum hits on top of a held tone don't break it up.
	SustainMedianSec float64
	SustainWindowSec float64
	SustainMaxCV     float64
	SustainMinBeats  float64
	SustainMinLevel  float64

	// AccentQuantile: low and high onsets that coincide and are both above
	// this strength quantile mark an accent (two-hand special candidate).
	AccentQuantile float64

	// DrumStemMinLevelDB: the beat phase is taken from the drum stem only if
	// its RMS is at least this many dB relative to the mix; quieter drum
	// stems (songs without drums: only leakage) leave the phase to the mix.
	// Measured: drums −6.2 dB (Pure Shores), −6.7 dB (Bonobo); a drumless
	// song hasn't been measured yet, so this is a wide margin.
	DrumStemMinLevelDB float64
}

// Default returns the default configuration.
func Default() Config {
	return Config{Mapper: DefaultMapper(), Export: ExportConfig{
		OggQuality:          6,
		CoverSize:           512,
		MaxAlignResidualSec: 0.002,
	}, Analysis: AnalysisConfig{
		SampleRate: 44100,
		WindowSize: 2048,
		HopSize:    512,
		Bands: []Band{
			{"low", 20, 150},
			{"mid", 150, 2000},
			{"high", 2000, 16000},
		},
		LogCompression:   10,
		ToneLowHz:        100,
		ToneHighHz:       4000,
		OnsetLatencySec:  0.012,
		MedianWindowSec:  0.5,
		ThresholdMult:    1.5,
		ThresholdDelta:   0.12,
		PeakWindowFrames: 3,
		MinOnsetGapSec:   0.05,

		MinBPM:         70,
		MaxBPM:         180,
		PriorCenterBPM: 120,
		PriorOctaves:   1.0,
		FineSearchPct:  1.0,
		FineStepBPM:    0.01,
		BandWeights:    []float64{1.0, 0.8, 0.6},

		SnapTolerance: 0.25,
		Subdivisions:  []int{1, 2, 4},

		BeatsPerBar:      4,
		PhraseBars:       4,
		SilenceDB:        40,
		CalmQuantile:     0.33,
		IntenseQuantile:  0.66,
		LabelToleranceDB: 1.5,
		DropRatio:        1.5,

		SustainMedianSec: 0.2,
		SustainWindowSec: 0.25,
		SustainMaxCV:     0.15,
		SustainMinBeats:  1,
		SustainMinLevel:  0.5,

		AccentQuantile: 0.8,

		DrumStemMinLevelDB: -20,
	}, Stems: StemsConfig{
		Model:              "htdemucs",
		MaxDurationDiffSec: 0.1,
		MaxSearchSec:       0.15,
		MinAlignCorr:       0.5,
		MaxOffsetSec:       0.002,
	}}
}
