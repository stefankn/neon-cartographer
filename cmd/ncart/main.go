// Command ncart generates Synth Riders maps from music files.
//
//	ncart [generate] -in song.mp3 [-out song.synth] [-difficulties easy,normal,hard,expert] ...
//	ncart analyze -in song.mp3 [-bpm 128] [-half-beat] [-debug analysis.json] [-clicks check.wav] [-beat-test beat.wav]
//	ncart validate song.synth [-difficulty hard]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stefankn/neon-cartographer/internal/analysis"
	"github.com/stefankn/neon-cartographer/internal/audio"
	"github.com/stefankn/neon-cartographer/internal/config"
	"github.com/stefankn/neon-cartographer/internal/generate"
	"github.com/stefankn/neon-cartographer/internal/mapper"
	"github.com/stefankn/neon-cartographer/internal/rules"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

const usage = `ncart — generate Synth Riders maps from music files

Usage:
  ncart [generate] -in song.mp3 [-out song.synth] [-difficulties easy,normal,hard,expert]
                   [-title ".."] [-artist ".."] [-mapper neon-cartographer] [-bpm 126] [-half-beat] [-offset-ms 0]
                   [-seed 42] [-walls]
  ncart analyze -in song.mp3 [-bpm 128] [-half-beat] [-debug analysis.json] [-clicks check.wav]
                [-beat-test beat.wav]
  ncart validate song.synth [-difficulty hard]

Commands:
  generate  write a .synth map (default command)
  analyze   decode and analyse a song, print a summary
  validate  check any .synth map against the playability rules

Run 'ncart <command> -h' for the command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd := os.Args[1]; {
	case strings.HasPrefix(cmd, "-") && cmd != "-h" && cmd != "-help" && cmd != "--help":
		err = runGenerate(os.Args[1:])
	case cmd == "generate":
		err = runGenerate(os.Args[2:])
	case cmd == "validate":
		err = runValidate(os.Args[2:])
	case cmd == "analyze":
		err = runAnalyze(os.Args[2:])
	case cmd == "-h" || cmd == "-help" || cmd == "--help" || cmd == "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "ncart: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ncart:", err)
		os.Exit(1)
	}
}

func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	in := fs.String("in", "", "input audio file (mp3, ogg, wav; other formats via ffmpeg)")
	bpm := fs.Float64("bpm", 0, "override tempo detection with this BPM")
	halfBeat := fs.Bool("half-beat", false, "move the beat grid by half a beat (if -beat-test shows the beat on the high tick)")
	debug := fs.String("debug", "", "write the full analysis as JSON to this file")
	clicks := fs.String("clicks", "", "write the song mixed with beat clicks to this WAV file (higher click = bar start)")
	beatTest := fs.String("beat-test", "", "write the song with a low tock on each detected beat and a high tick halfway between to this WAV file")
	fs.Parse(args)
	if *in == "" {
		fs.Usage()
		return fmt.Errorf("-in is required")
	}
	cfg := config.Default()

	t0 := time.Now()
	a, err := audio.Decode(*in, cfg.Analysis.SampleRate)
	if err != nil {
		return err
	}
	tDecode := time.Since(t0)
	t1 := time.Now()
	r, err := analysis.Analyze(a, cfg.Analysis, analysis.Overrides{BPM: *bpm, HalfBeat: *halfBeat})
	if err != nil {
		return err
	}
	tAnalyze := time.Since(t1)

	printSummary(*in, a, r)
	fmt.Printf("\nTiming: decode %.1fs, analysis %.1fs\n", tDecode.Seconds(), tAnalyze.Seconds())

	if *debug != "" {
		b, err := json.MarshalIndent(r, "", " ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*debug, b, 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote analysis JSON to", *debug)
	}
	if *clicks != "" {
		accent := make([]bool, len(r.Beats))
		for i := range accent {
			accent[i] = r.Grid.IsDownbeat(i)
		}
		mixed := audio.MixClicks(a.Samples, a.Rate, r.Beats, accent)
		if err := audio.WriteWAV(*clicks, mixed, a.Rate); err != nil {
			return err
		}
		fmt.Println("Wrote click track to", *clicks)
	}
	if *beatTest != "" {
		mixed := audio.MixBeatTest(a.Samples, a.Rate, r.Beats, r.Grid.Period())
		if err := audio.WriteWAV(*beatTest, mixed, a.Rate); err != nil {
			return err
		}
		fmt.Println("Wrote beat test to", *beatTest)
		fmt.Println("  Nod or clap along to the music. On the low tock: the beat grid is right.")
		if r.Tempo.HalfBeat {
			fmt.Println("  On the high tick: the grid is half a beat off; run without -half-beat.")
		} else {
			fmt.Println("  On the high tick: the grid is half a beat off; use -half-beat (generate and analyze).")
		}
	}
	return nil
}

func mmss(sec float64) string {
	m := int(sec) / 60
	return fmt.Sprintf("%d:%04.1f", m, sec-float64(m*60))
}

func printSummary(path string, a *audio.Audio, r *analysis.Result) {
	fmt.Printf("File:        %s (%s decoder, source %d Hz, %d ch)\n", path, a.Decoder, a.SourceRate, a.Channels)
	fmt.Printf("Duration:    %s\n", mmss(r.Duration))
	if r.Tempo.Overridden {
		fmt.Printf("Tempo:       %.2f BPM (from -bpm)\n", r.Tempo.BPM)
	} else {
		fmt.Printf("Tempo:       %.2f BPM (confidence %.2f; next-best unrelated tempo %.1f)\n", r.Tempo.BPM, r.Tempo.Confidence, r.Tempo.AltBPM)
	}
	shifted := ""
	if r.Tempo.HalfBeat {
		shifted = " (moved half a beat by -half-beat)"
	}
	fmt.Printf("Beat grid:   first beat at %.3f s%s, bars start on beat %d, %d beats, %d bars\n",
		r.Grid.FirstBeat, shifted, r.Grid.DownbeatIndex, len(r.Beats), len(r.Bars))
	fmt.Printf("Sound:       %s – %s\n", mmss(r.SoundStart), mmss(r.SoundEnd))

	perBand := func(count func(band int) int) string {
		var parts []string
		for b, name := range r.Bands {
			parts = append(parts, fmt.Sprintf("%s %d", name, count(b)))
		}
		return strings.Join(parts, ", ")
	}
	fmt.Printf("Onsets:      %s\n", perBand(func(b int) int {
		n := 0
		for _, o := range r.Onsets {
			if o.Band == b {
				n++
			}
		}
		return n
	}))
	for _, sub := range []int{1, 2, 4} {
		list := r.Snapped[sub]
		fmt.Printf("On 1/%d grid: %s\n", sub, perBand(func(b int) int {
			n := 0
			for _, s := range list {
				if s.Band == b {
					n++
				}
			}
			return n
		}))
	}
	fmt.Printf("Sustains:    %s (rail candidates)\n", perBand(func(b int) int {
		n := 0
		for _, s := range r.Sustains {
			if s.Band == b {
				n++
			}
		}
		return n
	}))
	fmt.Printf("Accents:     %d (strong low+high together)\n", len(r.Accents))
	var sil float64
	for _, s := range r.Silence {
		sil += s.End - s.Start
	}
	fmt.Printf("Silence:     %d ranges, %.1f s total\n", len(r.Silence), sil)

	fmt.Printf("\nPhrases (%d bars each):\n  %-9s %-17s %8s  %s\n", config.Default().Analysis.PhraseBars, "bars", "time", "energy", "label")
	for _, p := range r.Phrases {
		mark := ""
		if p.SectionStart {
			mark = "  ← section start"
		}
		energy := "    -"
		if p.Label != analysis.LabelSilent {
			energy = fmt.Sprintf("%5.1f dB", p.EnergyDB)
		}
		fmt.Printf("  %3d–%-5d %7s–%-9s %8s  %s%s\n", p.StartBar, p.EndBar-1, mmss(p.Start), mmss(p.End), energy, p.Label, mark)
	}
}

func runGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	in := fs.String("in", "", "input audio file (mp3, ogg, wav; other formats via ffmpeg)")
	out := fs.String("out", "", "output .synth file (default: next to the input)")
	diffs := fs.String("difficulties", "easy,normal,hard,expert", "comma-separated: easy,normal,hard,expert,master")
	title := fs.String("title", "", "song title (default: from tags, else file name)")
	artist := fs.String("artist", "", "artist (default: from tags)")
	mapperName := fs.String("mapper", "neon-cartographer", "mapper name shown in the game")
	bpm := fs.Float64("bpm", 0, "override tempo detection with this BPM")
	halfBeat := fs.Bool("half-beat", false, "move the beat grid by half a beat (if analyze -beat-test shows the beat on the high tick)")
	offsetMs := fs.Float64("offset-ms", 0, "move all notes later (positive) or earlier (negative) by this many ms")
	seed := fs.Int64("seed", 42, "random seed (output is deterministic for a given seed)")
	noCover := fs.Bool("no-cover", false, "don't embed the song's cover art")
	walls := fs.Bool("walls", false, "add a few side walls in calm sections (Hard and up)")
	debug := fs.String("debug", "", "also write the analysis as JSON to this file")
	fs.Parse(args)
	if *in == "" {
		fs.Usage()
		return fmt.Errorf("-in is required")
	}
	names, err := mapper.CanonicalDifficulties(*diffs)
	if err != nil {
		return err
	}
	start := time.Now()
	rep, err := generate.Run(generate.Options{
		In: *in, Out: *out, Difficulties: names, Title: *title, Artist: *artist, Mapper: *mapperName,
		BPM: *bpm, HalfBeat: *halfBeat, OffsetMs: *offsetMs, Seed: *seed, NoCover: *noCover, Walls: *walls,
	}, config.Default())
	if err != nil {
		if rep != nil && len(rep.Validation) > 0 {
			printValidation(rep.Validation, 5)
		}
		return err
	}
	r := rep.Analysis
	fmt.Printf("Song:      %s – %s (%s)\n", rep.Artist, rep.Title, mmss(r.Duration))
	fmt.Printf("Tempo:     %.2f BPM", r.Tempo.BPM)
	if r.Tempo.Overridden {
		fmt.Print(" (from -bpm)")
	}
	fmt.Printf(", first beat at %.3f s", r.Grid.FirstBeat)
	if r.Tempo.HalfBeat {
		fmt.Print(" (moved half a beat by -half-beat)")
	}
	fmt.Println()
	fmt.Printf("Audio:     %s, %.0f ms silence prepended so beats land on the grid (Offset 0)\n",
		rep.Encoder, rep.Timeline.PadSec*1000)
	fmt.Printf("Alignment: decoder shift %.1f ms corrected, residual %.2f ms (correlation %.2f)\n",
		rep.Timeline.ShiftSec*1000, rep.AlignResidualSec*1000, rep.AlignCorr)
	cover := "none"
	if rep.Cover {
		cover = "embedded from the song file"
	}
	fmt.Printf("Cover:     %s\n", cover)
	fmt.Printf("Bookmarks: %d section starts\n\n", len(rep.Map.Bookmarks))
	printValidation(rep.Validation, 5)
	fmt.Printf("Wrote %s (self-check OK) in %.1fs\n", rep.Out, time.Since(start).Seconds())
	if *debug != "" {
		b, err := json.MarshalIndent(r, "", " ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*debug, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	only := fs.String("difficulty", "", "check only this difficulty")
	maxList := fs.Int("list", 10, "list at most this many violations per difficulty")
	// accept the file before or after the flags
	var path string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	fs.Parse(args)
	if path == "" && fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	if path == "" {
		return fmt.Errorf("usage: ncart validate song.synth [-difficulty hard]")
	}
	f, err := synthfile.Read(path)
	if err != nil {
		return err
	}
	fmt.Printf("%s – %s, %.2f BPM, mapper %q\n", f.Beatmap.Author, f.Beatmap.Name, f.Beatmap.BPM, f.Beatmap.Beatmapper)
	bad := 0
	if probs := synthfile.Check(f); len(probs) > 0 {
		fmt.Printf("\nFormat problems (%d):\n", len(probs))
		for _, p := range probs {
			fmt.Println("  " + p.String())
		}
		bad += len(probs)
	}
	reps, err := rules.ValidateFile(f, config.Default().Mapper)
	if err != nil {
		return err
	}
	if *only != "" {
		var keep []rules.DifficultyReport
		for _, r := range reps {
			if strings.EqualFold(r.Name, *only) {
				keep = append(keep, r)
			}
		}
		if len(keep) == 0 {
			return fmt.Errorf("difficulty %q has no notes in this map", *only)
		}
		reps = keep
	}
	fmt.Println()
	bad += printValidation(reps, *maxList)
	if bad > 0 {
		return fmt.Errorf("%d problems found", bad)
	}
	fmt.Println("\nNo problems found.")
	return nil
}

// printValidation prints the per-difficulty table and the first violations;
// it returns the total number of violations.
func printValidation(reps []rules.DifficultyReport, maxList int) int {
	lim := config.Default().Mapper
	fmt.Printf("%-8s %6s %6s %8s %6s %9s %9s %24s %10s\n",
		"", "notes", "rails", "specials", "walls", "NPS avg", "NPS peak", "hand speed L/R (limit)", "violations")
	total := 0
	for _, r := range reps {
		s := r.Stats
		dc, _ := lim.Difficulty(r.Name)
		fmt.Printf("%-8s %6d %6d %8d %6d %9.2f %9.2f %10.2f/%-4.2f (%.1f) m/s %10d\n",
			r.Name, s.Notes, s.Rails, s.Specials, s.Walls, s.NPSAvg, s.NPSPeak, s.MaxSpeed[0], s.MaxSpeed[1], dc.MaxHandSpeed, len(r.Violations))
		total += len(r.Violations)
	}
	for _, r := range reps {
		if len(r.Violations) == 0 {
			continue
		}
		fmt.Printf("\n%s violations (%d):\n", r.Name, len(r.Violations))
		for i, v := range r.Violations {
			if i == maxList {
				fmt.Printf("  … and %d more\n", len(r.Violations)-maxList)
				break
			}
			fmt.Println("  " + v.String())
		}
	}
	return total
}
