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

	// MetricWeight is added to a slot's score by position in the bar, so
	// strong beats win over off-beats at equal onset strength.
	DownbeatWeight, BeatWeight, HalfWeight, QuarterWeight float64

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

		DownbeatWeight: 0.6, BeatWeight: 0.4, HalfWeight: 0.2, QuarterWeight: 0,

		SpecialX: 0, SpecialY: 1,

		RailNodeBeats:   0.5,
		RailMaxBeats:    8,
		RailSpeedFactor: 0.5,
		RailMinLevelDB:  -12,

		WallEveryPhrases: 2,
		WallClearBeats:   1,

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
