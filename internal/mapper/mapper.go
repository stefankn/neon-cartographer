// Package mapper turns an analysis into notes per difficulty.
package mapper

import (
	"fmt"
	"math"
	"strings"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Note is one orb (or rail head) in song beats and editor grid cells.
type Note struct {
	Beat float64            // song beat counted from the start of the exported audio
	Type synthfile.NoteType // hand / special
	X, Y float64            // editor grid cells
	Rail []RailPoint        // further rail nodes; nil for a plain note
}

// RailPoint is a rail node.
type RailPoint struct{ Beat, X, Y float64 }

// Difficulty holds the notes of one difficulty in time order.
type Difficulty struct {
	Name  string // canonical: Easy, Normal, Hard, Expert, Master
	Notes []Note
	Walls []Wall
}

// Bookmark marks a section in the editor.
type Bookmark struct {
	Beat float64
	Name string
}

// Map is the mapper's output.
type Map struct {
	BPM          float64
	Difficulties []Difficulty
	Bookmarks    []Bookmark
}

// CanonicalDifficulties parses a comma-separated list like "normal,hard".
func CanonicalDifficulties(list string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		found := ""
		for _, d := range synthfile.Difficulties {
			if strings.EqualFold(d, p) && d != "Custom" {
				found = d
			}
		}
		if found == "" {
			return nil, fmt.Errorf("unknown difficulty %q (use easy, normal, hard, expert, master)", p)
		}
		if !seen[found] {
			seen[found] = true
			out = append(out, found)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no difficulties given")
	}
	return out, nil
}

// Timeline relates the analysed audio to the exported audio. The exported
// audio is the original with PadSec of silence prepended, chosen so every
// grid beat falls on a whole song beat (song beat 0 = start of audio). That
// way the map needs Offset 0 and all tick keys are integers.
type Timeline struct {
	BPM    float64
	PadSec float64 // silence prepended to the exported audio
	// ShiftSec: extra delay of the music in the exported audio beyond PadSec
	// (decoder start-delay differences measured after encoding).
	ShiftSec float64
	// OffsetSec moves notes later relative to the music (-offset-ms).
	OffsetSec float64
	grid      analysis.Grid
	beatShift float64 // song beat of grid beat 0
}

// NewTimeline computes the padding for a grid. shiftSec is the measured
// decoder delay (0 before measuring), offsetSec the user's note offset.
func NewTimeline(g analysis.Grid, offsetSec, shiftSec float64) Timeline {
	p := g.Period()
	first := g.FirstBeat + offsetSec + shiftSec
	pad := math.Mod(p-math.Mod(first, p)+2*p, p)
	if pad > p-1e-9 {
		pad = 0
	}
	return Timeline{
		BPM: g.BPM, PadSec: pad, ShiftSec: shiftSec, OffsetSec: offsetSec,
		grid: g, beatShift: math.Round((first + pad) / p),
	}
}

// Period is the beat length in seconds.
func (tl Timeline) Period() float64 { return 60 / tl.BPM }

// SongBeat converts a grid beat index (analysis grid) to a song beat.
func (tl Timeline) SongBeat(gridBeat float64) float64 { return gridBeat + tl.beatShift }

// ExportTime converts a time in the analysed audio to the exported audio.
func (tl Timeline) ExportTime(t float64) float64 { return t + tl.PadSec + tl.ShiftSec }

// SongBeatTime is the exported-audio time of a song beat.
func (tl Timeline) SongBeatTime(beat float64) float64 { return beat * tl.Period() }

func inSilence(ranges []analysis.Range, t float64) bool {
	for _, s := range ranges {
		if t >= s.Start && t < s.End {
			return true
		}
	}
	return false
}

// Model converts a difficulty to the rules model (seconds, grid cells) on
// the exported-audio timeline.
func (d Difficulty) Model(tl Timeline) rules.Model {
	var m rules.Model
	for _, n := range d.Notes {
		rn := rules.Note{T: tl.SongBeatTime(n.Beat), X: n.X, Y: n.Y, Type: n.Type}
		for _, p := range n.Rail {
			rn.Rail = append(rn.Rail, rules.Point{T: tl.SongBeatTime(p.Beat), X: p.X, Y: p.Y})
		}
		m.Notes = append(m.Notes, rn)
	}
	for _, w := range d.Walls {
		m.Walls = append(m.Walls, rules.Wall{T: tl.SongBeatTime(w.Beat), SlideType: w.SlideType})
	}
	return m
}
