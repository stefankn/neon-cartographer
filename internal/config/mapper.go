package config

// MapperConfig tunes note placement. Positions are in editor grid cells:
// 1 cell = 0.1365 m, x to the right, y up, (0, 0) ≈ chest height in front
// of the player. The editor grid spans about x −8…8, y −6…6.
type MapperConfig struct {
	// LeadInSec: no notes before this time in the exported audio, and none
	// before the music starts.
	LeadInSec float64

	Geometry Geometry

	// Difficulties holds the per-difficulty rules, keyed by editor name
	// (Easy, Normal, Hard, Expert, Master). Custom uses Master's.
	Difficulties map[string]DifficultyConfig

	// Hand for each analysis band (0 = low, 1 = mid, 2 = high):
	// -1 = left, +1 = right, 0 = no preference (alternate).
	BandHand []int
	// BandDominance: a band "owns" a slot when its onset strength is at
	// least this many times the other side's.
	BandDominance float64
	// PairStrength: low and high onsets both at least this strong
	// (0..1) at the same slot become a simultaneous two-hand pair
	// (difficulties with Pairs enabled).
	PairStrength float64

	// Metric weights are added to a slot's score by position in the bar so
	// main beats are kept before off-beats: beat 1 (Downbeat), beat 3 in
	// 4/4 (MidBar), the other beats (Beat), half-beat off-beats (Half),
	// 1/4 positions (Quarter). The steps are small next to onset strength
	// (0..1 per band): a clearly louder off-beat (syncopation) still wins
	// over a weak beat, but similar sounds go to the stronger position.
	DownbeatWeight, MidBarWeight, BeatWeight, HalfWeight, QuarterWeight float64

	// Rhythm: each bar's positions are ranked by their mean onset strength
	// over all bars with a similar rhythm, so a repeating musical figure
	// becomes a repeating note figure. RhythmMinStrength: total onset
	// strength for a position to count in a bar's rhythm signature
	// (strength per position). RhythmSimilarity: cosine similarity of two
	// signatures for one bar to count towards the other's ranking, and for
	// a bar to start from the order of an earlier, similar bar.
	// RhythmTieMargin: in that case a position only moves ahead of another
	// when its score is higher by more than this, so near ties resolve the
	// same way in every repetition.
	RhythmMinStrength float64
	RhythmSimilarity  float64
	RhythmTieMargin   float64

	// Specials: BothHandsSpecial notes are placed at (SpecialX, SpecialY).
	SpecialX, SpecialY float64

	// Rails.
	// RailNodeBeats: spacing of rail nodes. RailMaxBeats caps a rail's
	// length. RailSpeedFactor: a rail's own movement uses at most this
	// fraction of the hand-speed limit. RailMinLevelDB: ignore sustains
	// quieter than this (relative to the band's loudest frame).
	RailNodeBeats   float64
	RailMaxBeats    float64
	RailSpeedFactor float64
	RailMinLevelDB  float64

	// Walls (only with -walls, only on difficulties with WallsAllowed).
	// WallEveryPhrases: at most one wall per this many calm phrases.
	// WallClearBeats: no note (or rail) within ± this many beats of a wall.
	WallEveryPhrases int
	WallClearBeats   float64

	// Positions follow the music (patterns.go):
	// ToneWeight: share of a note's height that comes from its tone (the
	// rest from the phrase pattern). ToneRangeY: cells above/below HomeY for
	// the highest/lowest tones. Tones are normalised per song between the
	// ToneLowQuantile and ToneHighQuantile tones of the selected notes, so
	// a few outliers don't squash the range.
	// SpreadMin: move size (pattern offsets and tone height) at QuietDB or
	// quieter, as a fraction of the full size reached at the loudest bar;
	// in between it grows linearly with the beat's level in dB.
	ToneWeight                        float64
	ToneRangeY                        float64
	ToneLowQuantile, ToneHighQuantile float64
	SpreadMin                         float64
	QuietDB                           float64
	// RepeatToneOctaves: a bar repeats the note positions of an earlier,
	// rhythmically similar bar of its phrase only if their mean tone
	// differs by at most this much (octaves); otherwise its notes follow
	// their own tones, so a melody over a steady groove still moves.
	RepeatToneOctaves float64

	// PlaceSearchRadius: when a note's pattern position breaks a rule, the
	// nearest grid cell within this radius that satisfies every rule is
	// used; if there is none the note is dropped.
	PlaceSearchRadius float64
}

// Geometry is the playable space and hand-motion limits shared by all
// difficulties.
type Geometry struct {
	// Comfort zone (grid cells): roughly arm's reach, shoulder to head.
	XMin, XMax, YMin, YMax float64
	// HomeX: hands rest at x = ±HomeX, y = HomeY.
	HomeX, HomeY float64
	// MinSimultaneousSep: notes for the two hands at the same moment must be
	// at least this far apart (cells).
	MinSimultaneousSep float64
	// ReachSlackM: movement up to this many metres is always allowed, on
	// top of speed × time (a small reach needs no travel time).
	ReachSlackM float64
}

// DifficultyConfig holds the rules for one difficulty.
type DifficultyConfig struct {
	// Subdivision: finest grid notes are placed on (1 = beats, 2 = 1/2,
	// 4 = 1/4 beats).
	Subdivision int
	// MinGapBeats: minimum time between two notes (any hand).
	MinGapBeats float64
	// StrongGapBeats/StrongQuantile: in intense phrases, onsets in the top
	// (1−StrongQuantile) strength may use this smaller gap and the 1/4
	// grid even when Subdivision is coarser. 0 disables.
	StrongGapBeats float64
	StrongQuantile float64
	// Density: target notes per beat by section label (calm/normal/intense).
	// Applied per bar: round(Density × beats per bar) notes, at least 1.
	Density map[string]float64
	// MaxHandSpeed in metres per second between consecutive touches of
	// one hand (plus Geometry.ReachSlackM).
	MaxHandSpeed float64
	// MaxSameHandRun: at most this many consecutive notes for one hand.
	MaxSameHandRun int
	// CrossoverCells: how far a hand may reach past the centre line to the
	// other side (0 = never).
	CrossoverCells float64
	// Pairs: strong low+high hits together become a two-hand pair.
	Pairs bool
	// AccentSpecialBars: minimum bars between BothHandsSpecial notes on
	// accents (0 = specials only at section starts).
	AccentSpecialBars float64
	// RailMinBeats: shortest sustain that becomes a rail (0 = no rails).
	RailMinBeats float64
	// MaxRailsPerPhrase caps rails per phrase.
	MaxRailsPerPhrase int
	// WallsAllowed: -walls may add walls to this difficulty.
	WallsAllowed bool
}

// Section labels used in Density (match analysis labels).
const (
	Calm    = "calm"
	Normal  = "normal"
	Intense = "intense"
)

// DefaultMapper returns the default mapper configuration. The difficulty
// values are starting points meant to be tuned by playing.
func DefaultMapper() MapperConfig {
	return MapperConfig{
		LeadInSec: 2,
		Geometry: Geometry{
			XMin: -6, XMax: 6, YMin: -3, YMax: 4,
			HomeX: 3, HomeY: 0,
			MinSimultaneousSep: 3,
			ReachSlackM:        0.10,
		},
		Difficulties: map[string]DifficultyConfig{
			"Easy": {
				Subdivision: 1, MinGapBeats: 1,
				Density:      map[string]float64{Calm: 0.25, Normal: 0.4, Intense: 0.5},
				MaxHandSpeed: 1.0, MaxSameHandRun: 2,
				RailMinBeats: 2, MaxRailsPerPhrase: 1,
			},
			"Normal": {
				Subdivision: 2, MinGapBeats: 0.5,
				Density:      map[string]float64{Calm: 0.4, Normal: 0.7, Intense: 1.0},
				MaxHandSpeed: 1.5, MaxSameHandRun: 3,
				AccentSpecialBars: 2, RailMinBeats: 2, MaxRailsPerPhrase: 1,
			},
			"Hard": {
				Subdivision: 2, MinGapBeats: 0.5, StrongGapBeats: 0.25, StrongQuantile: 0.7,
				Density:      map[string]float64{Calm: 0.6, Normal: 1.0, Intense: 1.4},
				MaxHandSpeed: 2.0, MaxSameHandRun: 4, CrossoverCells: 2, Pairs: true,
				AccentSpecialBars: 1, RailMinBeats: 1.5, MaxRailsPerPhrase: 1, WallsAllowed: true,
			},
			"Expert": {
				Subdivision: 4, MinGapBeats: 0.25,
				Density:      map[string]float64{Calm: 0.8, Normal: 1.4, Intense: 2.0},
				MaxHandSpeed: 2.6, MaxSameHandRun: 6, CrossoverCells: 2, Pairs: true,
				AccentSpecialBars: 1, RailMinBeats: 1.5, MaxRailsPerPhrase: 2, WallsAllowed: true,
			},
			"Master": {
				Subdivision: 4, MinGapBeats: 0.25,
				Density:      map[string]float64{Calm: 1.0, Normal: 1.8, Intense: 2.6},
				MaxHandSpeed: 3.2, MaxSameHandRun: 8, CrossoverCells: 2, Pairs: true,
				AccentSpecialBars: 1, RailMinBeats: 1, MaxRailsPerPhrase: 2, WallsAllowed: true,
			},
		},
		BandHand:      []int{-1, 0, 1},
		BandDominance: 1.5,
		PairStrength:  0.6,

		DownbeatWeight: 0.9, MidBarWeight: 0.75, BeatWeight: 0.6, HalfWeight: 0.3, QuarterWeight: 0,

		RhythmMinStrength: 0.3,
		RhythmTieMargin:   0.15,
		RhythmSimilarity:  0.75,

		SpecialX: 0, SpecialY: 1,

		RailNodeBeats:   0.5,
		RailMaxBeats:    8,
		RailSpeedFactor: 0.5,
		RailMinLevelDB:  -12,

		WallEveryPhrases: 2,
		WallClearBeats:   1,

		ToneWeight: 0.7, ToneRangeY: 3.5,
		ToneLowQuantile: 0.1, ToneHighQuantile: 0.9,
		SpreadMin: 0.3, QuietDB: -18,
		RepeatToneOctaves: 0.25,

		PlaceSearchRadius: 6,
	}
}

// Difficulty returns the rules for a difficulty name (Custom → Master).
func (m MapperConfig) Difficulty(name string) (DifficultyConfig, bool) {
	if name == "Custom" {
		name = "Master"
	}
	d, ok := m.Difficulties[name]
	return d, ok
}
