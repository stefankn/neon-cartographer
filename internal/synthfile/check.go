package synthfile

import (
	"fmt"
	"math"
	"slices"
	"strconv"
)

// Problem is one structural issue found by Check.
type Problem struct {
	Difficulty string // "" for map-level problems
	Where      string // tick key, note Id, or entry name
	Msg        string
}

func (p Problem) String() string {
	if p.Difficulty == "" {
		return fmt.Sprintf("%s: %s", p.Where, p.Msg)
	}
	return fmt.Sprintf("%s %s: %s", p.Difficulty, p.Where, p.Msg)
}

// Tolerances for Check. The editor stores 32-bit floats.
const (
	zTolerance    = 1e-3 // z units (1/20 s): 0.05 ms
	gridTolerance = 1e-3 // grid cells
)

// Check verifies the structural rules from docs/synth-format.md: the audio
// entry exists, difficulty names are valid, tick keys are numeric and agree
// with each note's z, positions sit on the editor grid, there is at most one
// note of each type per tick, rails move forward in time and walls' z agrees
// with their time. Playability rules are checked elsewhere.
func Check(f *File) []Problem {
	var out []Problem
	add := func(diff, where, format string, args ...any) {
		out = append(out, Problem{diff, where, fmt.Sprintf(format, args...)})
	}
	bm := f.Beatmap
	if bm == nil {
		return []Problem{{Where: BeatmapEntry, Msg: "missing"}}
	}
	if bm.BPM <= 0 {
		add("", "BPM", "must be > 0, is %v", bm.BPM)
		return out
	}
	if f.Entry(bm.AudioName) == nil {
		add("", "AudioName", "%q has no zip entry", bm.AudioName)
	}
	zOf := func(tick float64) float64 { return SecondsToZ(bm.BeatToSeconds(tick / TicksPerBeat)) }

	for diff, ticks := range bm.Track {
		if !slices.Contains(Difficulties, diff) {
			add("", "Track", "unknown difficulty %q", diff)
		}
		for key, list := range ticks {
			tick, err := strconv.ParseFloat(key, 64)
			if err != nil {
				add(diff, key, "tick key is not a number")
				continue
			}
			types := map[NoteType]bool{}
			for _, n := range list {
				if want := zOf(tick); math.Abs(n.Position[2]-want) > zTolerance {
					add(diff, n.ID, "z=%v but tick %s implies %v", n.Position[2], key, want)
				}
				gx, gy := MetresToGrid(n.Position[0], n.Position[1])
				if math.Abs(gx-math.Round(gx)) > gridTolerance || math.Abs(gy-math.Round(gy)) > gridTolerance {
					add(diff, n.ID, "position (%.4f, %.4f) is off the editor grid", gx, gy)
				}
				if n.Type < RightHanded || n.Type > BothHandsSpecial {
					add(diff, n.ID, "invalid note type %d", n.Type)
				}
				if types[n.Type] {
					add(diff, key, "more than one %v note at this tick", n.Type)
				}
				types[n.Type] = true
				prev := n.Position[2]
				for _, s := range n.Segments {
					if s[2] <= prev {
						add(diff, n.ID, "rail segments must increase in z")
						break
					}
					prev = s[2]
				}
			}
		}
	}
	for _, walls := range []map[string][]Wall{bm.Slides, bm.Crouchs, bm.Squares, bm.Triangles} {
		for diff, list := range walls {
			for _, w := range list {
				if want := zOf(w.Time); math.Abs(w.Position[2]-want) > zTolerance {
					add(diff, fmt.Sprintf("wall@%v", w.Time), "z=%v but time implies %v", w.Position[2], want)
				}
			}
		}
	}
	return out
}
