package rules

import (
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// DifficultyReport is the validation result of one difficulty.
type DifficultyReport struct {
	Name       string
	Stats      Stats
	Violations []Violation
}

// ValidateFile checks every difficulty of a map that has notes or walls
// against the rules for that difficulty.
func ValidateFile(f *synthfile.File, cfg config.MapperConfig) ([]DifficultyReport, error) {
	var out []DifficultyReport
	for _, name := range synthfile.Difficulties {
		m := FromSynth(f, name)
		if len(m.Notes) == 0 && len(m.Walls) == 0 {
			continue
		}
		l, err := LimitsFor(cfg, name)
		if err != nil {
			return nil, err
		}
		out = append(out, DifficultyReport{Name: name, Stats: ComputeStats(m, l.ReachSlackM), Violations: Check(m, l)})
	}
	return out, nil
}
