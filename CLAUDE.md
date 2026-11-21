# CLAUDE.md — neon-cartographer (command: ncart)

Go CLI that turns a music file into a Synth Riders custom map (`.synth`) for the official Beatmap Editor
and Meta Quest. User-facing docs: `README.md`. File format: `docs/synth-format.md`.

## Commands

`just check` runs vet + gofmt (fails on output) + tests — run it before reporting work as done.
`just demo` / `just analyze` wrap the test-song commands below (outputs in `out/`). Plain commands:

```
go test ./...          # all tests; end-to-end test needs sox (or ffmpeg)
go vet ./...
gofmt -l .             # must print nothing
go run ./cmd/ncart -in testdata/bonobo-drift.mp3 -out out/bonobo-drift.synth -difficulties easy,normal,hard,expert,master -walls
go run ./cmd/ncart validate out/bonobo-drift.synth
go run ./cmd/ncart analyze -in testdata/bonobo-drift.mp3 -debug /tmp/a.json -clicks /tmp/check.wav
```

Toolchain: Go 1.24+ (gonum v0.17 requires it). External tools: **sox** (required for Ogg export; the
Homebrew ffmpeg here has *no* libvorbis), ffmpeg/ffprobe (optional: tags, cover art, other formats).

## Layout and data flow

```
cmd/ncart             CLI: generate (default), analyze, validate — stdlib flag, subcommand switch
internal/config       ALL tunables: config.go (analysis, export), mapper.go (difficulty table, geometry)
internal/audio        decode (mp3/ogg/wav native, ffmpeg fallback), resample, EncodeOgg (sox → ffmpeg
                      libvorbis → ffmpeg native), MeasureOffset (envelope cross-correlation), tags/cover
internal/analysis     STFT → band flux → onsets; tempo/grid; energy, phrases, sustains, accents → Result
internal/mapper       Timeline (padding/beat mapping) + Generate: slots → density → specials → rails →
                      hands → patterns → place (rules-constrained) → walls → enforce
internal/rules        hand-motion model, Check (violations), Stats, FromSynth, ValidateFile
internal/export       mapper.Map → synthfile.File in editor-1.9.7 style
internal/synthfile    .synth read/write/Check; lossless round trip (unknown fields kept)
internal/generate     the whole pipeline (Run), used by the CLI and the end-to-end test
reference/            editor-sample.synth — saved by the official editor 1.9.7; ground truth for tests.
                      JSON entries are untouched editor output; the audio is replaced by 1 s of silence
                      (copyright) and the zip was rebuilt — see docs/synth-format.md
testdata/             bonobo-drift.mp3 — test song (126 BPM); local only, git-ignored (copyrighted)
out/                  generated maps (not source)
```

Pipeline: `audio.Decode` → `analysis.Analyze` → `mapper.NewTimeline` → `audio.EncodeOgg` with padding →
decode the Ogg and `MeasureOffset` against the original (re-encode once if > 2 ms off) →
`mapper.Generate` → `export.Build` → write → re-read → `synthfile.Check` + `rules.ValidateFile` (any
violation fails generation).

## Verified facts — do not break without re-testing in the editor

Confirmed by the user in Beatmap Editor 1.9.7 and on a Quest 3:
- Generated maps load with title/artist/cover; notes are on grid lines and on the beat; bookmarks work.
- **Key set:** write exactly what editor 1.9.7 writes (`synthfile.NewBeatmapEditor197`: no `Squares`,
  `Triangles`, `BeatModified`, `Explicit`). The codec writes back only the keys that were read (presence
  tracking in `synthfile/extra.go`).
- **Offset is always 0.** Instead, < 1 beat of silence is prepended to the audio so beats land on whole
  beats from audio start. All tick keys are then integers. The real semantics of `Offset` were never
  tested; avoid relying on them.
- Encodings: `beatmap.meta.bin` is JSON, UTF-8 with BOM, CRLF, 2-space indent; `track.data.json` is
  UTF-16LE with BOM, 4-space indent. The audio must be Ogg Vorbis, and `AudioName` decides which entry is used.
- Units: 64 ticks per beat; `Position` z = seconds × 20; x/y in metres = grid × 0.1365 (+0.002 / +0.0012
  snap offset); notes must be on whole grid cells. Note `Id` = `Note_<tick><TypeString><running n>`.
- Quest folder: `SynthRidersUC/CustomSongs` (confirmed by the user).

## Gotchas learned

- Maps downloaded from synthriderz.com are **AES-encrypted** zips → `synthfile.ErrEncrypted`. Only
  editor-saved custom maps are readable.
- Onsets are detected ~1 hop early (centred frames) → `OnsetLatencySec` 0.012 is added to onsets, the beat
  phase and sustains.
- `LogCompression` γ=10 (not 100) plus `ThresholdDelta` 0.12 stops kicks from showing up as hi-hat onsets
  and vice versa.
- One BPM must hold for the whole song: the fine search (0.01 BPM, phase scored over the whole song) is
  essential; 0.1 BPM error drifts ~0.45 beat over 4.5 minutes.
- Fast songs may be detected at half tempo (e.g. 87 for 174); same grid, `-bpm` overrides.
- Different MP3 decoders differ in start delay; that's why the export measures alignment instead of
  trusting it.
- The editor stores 32-bit floats (`0.00120002031`): compare positions with tolerances.
- Notes during the *other* hand's rail don't count toward same-hand runs (rules and mapper agree).

## Conventions

- Every tuning value goes in `internal/config` with a comment; no magic numbers in algorithms.
- Mapper and `validate` share `internal/rules`. Generated maps must have **zero** violations (tests
  enforce it); fix the mapper rather than loosening a check.
- Tests use deterministic synthetic signals (`analysis/synth_test.go`) and a synthetic
  `analysis.Result` (`mapper/mapper_test.go: richResult`); file-based tests skip if the reference file or
  sox/ffmpeg is missing.
- Output is deterministic for a given `-seed`.
- Downloaded or extracted third-party files go in the scratchpad, never into the repo.

## Status and next steps

Milestones 1–4 (format, analysis, first playable map, full mapper + validate + README) are done. The user
plays the maps and reported that they "need some tweaking". Next session: collect concrete play feedback
and tune `internal/config/mapper.go`. Known candidates:
- Fastest moves sit at the hand-speed limit on every difficulty (patterns get pulled in) → possibly
  smaller pattern offsets or lower density.
- Many rails on this song (Easy ≈ 12 % of notes) → `RailMinLevelDB` / `RailMinBeats`.
- Accent specials on Hard+ may be frequent (≤ 1 per bar).

Working style: the user plans each piece of work first, then expects implementation and a stop with
results plus concrete things to check (editor, Quest, listening).
