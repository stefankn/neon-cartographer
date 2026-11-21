// Package generate runs the whole pipeline: decode → analyse → encode the
// map audio → place notes → write and self-check the .synth file.
package generate

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/export"
	"github.com/stefankn/neon-cartographer/internal/mapper"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Options are the user's choices (CLI flags).
type Options struct {
	In, Out       string
	Difficulties  []string // canonical names
	Title, Artist string   // empty: from tags, else file name
	Mapper        string
	BPM           float64 // 0: detect
	OffsetMs      float64 // moves notes later relative to the music
	Seed          int64
	NoCover       bool
	Walls         bool // add a few side walls in calm sections (Hard and up)
}

// Report describes what was generated.
type Report struct {
	Out              string
	Title, Artist    string
	Analysis         *analysis.Result
	Timeline         mapper.Timeline
	Encoder          string
	AlignResidualSec float64 // remaining music offset vs. the intended timeline
	AlignCorr        float64
	Cover            bool
	NotesPerDiff     map[string]int
	Map              *mapper.Map
	Validation       []rules.DifficultyReport // of the written file
}

// Run executes the pipeline.
func Run(o Options, cfg config.Config) (*Report, error) {
	if o.Out == "" {
		o.Out = strings.TrimSuffix(o.In, filepath.Ext(o.In)) + ".synth"
	}
	rep := &Report{Out: o.Out, NotesPerDiff: map[string]int{}}

	src, err := audio.Decode(o.In, cfg.Analysis.SampleRate)
	if err != nil {
		return nil, err
	}
	r, err := analysis.Analyze(src, cfg.Analysis, o.BPM)
	if err != nil {
		return nil, err
	}
	rep.Analysis = r

	// Encode the map audio with padding, then measure where the music
	// really landed (decoders differ in MP3 start delay) and fix it once.
	tmp := o.Out + ".tmp.ogg"
	defer os.Remove(tmp)
	offset := o.OffsetMs / 1000
	tl := mapper.NewTimeline(r.Grid, offset, 0)
	var ogg *audio.Audio
	for attempt := 0; ; attempt++ {
		rep.Encoder, err = audio.EncodeOgg(o.In, tmp, tl.PadSec, cfg.Export.OggQuality)
		if err != nil {
			return nil, err
		}
		ogg, err = audio.Decode(tmp, cfg.Analysis.SampleRate)
		if err != nil {
			return nil, fmt.Errorf("decoding the encoded audio: %w", err)
		}
		expected := tl.PadSec + tl.ShiftSec
		lag, corr := audio.MeasureOffset(src.Samples, ogg.Samples, ogg.Rate, expected, 0.15)
		rep.AlignResidualSec, rep.AlignCorr = lag-expected, corr
		if corr < 0.5 {
			return nil, fmt.Errorf("could not align the encoded audio with the original (correlation %.2f)", corr)
		}
		if math.Abs(rep.AlignResidualSec) <= cfg.Export.MaxAlignResidualSec || attempt == 1 {
			break
		}
		// music sits at pad + shift + residual: re-plan with the measured shift
		tl = mapper.NewTimeline(r.Grid, offset, tl.ShiftSec+rep.AlignResidualSec)
	}
	if math.Abs(rep.AlignResidualSec) > 2*cfg.Export.MaxAlignResidualSec {
		return nil, fmt.Errorf("encoded audio is still %.1f ms off after correction", rep.AlignResidualSec*1000)
	}
	rep.Timeline = tl
	oggBytes, err := os.ReadFile(tmp)
	if err != nil {
		return nil, err
	}

	// Metadata.
	tags := audio.ProbeTags(o.In)
	base := strings.TrimSuffix(filepath.Base(o.In), filepath.Ext(o.In))
	rep.Title = firstNonEmpty(o.Title, tags.Title, base)
	rep.Artist = firstNonEmpty(o.Artist, tags.Artist, "Unknown artist")
	var cover []byte
	if !o.NoCover {
		if png, ok := audio.ExtractCover(o.In, cfg.Export.CoverSize); ok {
			cover, rep.Cover = png, true
		}
	}

	// Notes.
	m, err := mapper.Generate(r, tl, mapper.Options{Difficulties: o.Difficulties, Seed: o.Seed, Walls: o.Walls}, cfg.Mapper)
	if err != nil {
		return nil, err
	}
	rep.Map = m
	for _, d := range m.Difficulties {
		rep.NotesPerDiff[d.Name] = len(d.Notes)
	}

	f, err := export.Build(m, export.Meta{
		Title: rep.Title, Artist: rep.Artist, Mapper: firstNonEmpty(o.Mapper, "neon-cartographer"),
		Duration: ogg.Duration(), SampleRate: ogg.SourceRate, Channels: ogg.Channels,
	}, oggBytes, cover)
	if err != nil {
		return nil, err
	}
	if err := f.Write(o.Out); err != nil {
		return nil, err
	}

	// Self-check: read back what was written.
	back, err := synthfile.Read(o.Out)
	if err != nil {
		return nil, fmt.Errorf("re-reading %s: %w", o.Out, err)
	}
	if probs := synthfile.Check(back); len(probs) > 0 {
		return nil, fmt.Errorf("written map fails self-check: %v", probs)
	}
	rep.Validation, err = rules.ValidateFile(back, cfg.Mapper)
	if err != nil {
		return nil, err
	}
	for _, d := range rep.Validation {
		if len(d.Violations) > 0 {
			return rep, fmt.Errorf("%s has %d rule violations, first: %v", d.Name, len(d.Violations), d.Violations[0])
		}
	}
	return rep, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
