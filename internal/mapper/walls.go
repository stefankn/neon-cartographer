package mapper

import (
	"math"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// placeWalls adds side walls in calm phrases only: at most one per
// WallEveryPhrases calm phrases, alternating right/left, on a whole beat
// with no note within ±WallClearBeats. If a phrase has no such gap, one is
// made by removing plain notes around the wall (never rails or specials).
// It returns the walls and the remaining notes.
func placeWalls(r *analysis.Result, tl Timeline, notes []rules.Note, cfg config.MapperConfig) ([]Wall, []rules.Note) {
	win := cfg.WallClearBeats * tl.Period()
	near := func(t float64, n rules.Note) bool { return t >= n.T-win-1e-9 && t <= n.End()+win+1e-9 }
	clear := func(t float64) bool {
		for _, n := range notes {
			if near(t, n) {
				return false
			}
		}
		return true
	}
	clearable := func(t float64) bool {
		for _, n := range notes {
			if near(t, n) && (len(n.Rail) > 0 || n.Type == synthfile.BothHandsSpecial) {
				return false
			}
		}
		return true
	}
	var walls []Wall
	calm := 0
	for _, p := range r.Phrases {
		if p.Label != analysis.LabelCalm {
			continue
		}
		calm++
		if (calm-1)%cfg.WallEveryPhrases != 0 {
			continue
		}
		start := math.Ceil(r.Grid.BeatAt(p.Start - tl.OffsetSec))
		end := math.Floor(r.Grid.BeatAt(p.End - tl.OffsetSec))
		var beats []float64
		for gb := start + 2; gb < end-1; gb++ {
			if playable(r, tl, cfg, gb) {
				beats = append(beats, gb)
			}
		}
		chosen := -1.0
		for _, gb := range beats { // a natural gap first
			if clear(tl.SongBeatTime(tl.SongBeat(gb))) {
				chosen = gb
				break
			}
		}
		if chosen < 0 {
			for _, gb := range beats {
				if t := tl.SongBeatTime(tl.SongBeat(gb)); clearable(t) {
					chosen = gb
					kept := notes[:0:0]
					for _, n := range notes {
						if !near(t, n) {
							kept = append(kept, n)
						}
					}
					notes = kept
					break
				}
			}
		}
		if chosen >= 0 {
			walls = append(walls, Wall{Beat: tl.SongBeat(chosen), SlideType: len(walls) % 2})
		}
	}
	return walls, notes
}
