# CLAUDE.md — neon-cartographer (command: ncart)

Go CLI that turns a music file into a Synth Riders custom map (`.synth`) for the official Beatmap Editor
and Meta Quest. User-facing docs: `README.md`. File format: `docs/synth-format.md`.

## Commands

`just check` runs vet + gofmt (fails on output) + tests — run it before reporting work as done.
`just demo` / `just analyze` wrap the test-song commands below (outputs in `out/`); `just build` /
`just test` build `./ncart` and run the tests. Plain commands:

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
internal/mapper       Timeline (padding/beat mapping) + Generate: slots → per-bar selection → specials →
                      rails → hands → patterns → place (rules-constrained) → walls → enforce
internal/rules        hand-motion model, Check (violations), Stats, FromSynth, ValidateFile
internal/export       mapper.Map → synthfile.File in editor-1.9.7 style
internal/synthfile    .synth read/write/Check; lossless round trip (unknown fields kept)
internal/generate     the whole pipeline (Run), used by the CLI and the end-to-end test
reference/            editor-sample.synth — saved by the official editor 1.9.7; ground truth for tests.
                      JSON entries are untouched editor output; the audio is replaced by 1 s of silence
                      (copyright) and the zip was rebuilt — see docs/synth-format.md
testdata/             bonobo-drift.mp3 (126 BPM), all_saints-pure_shores.mp3 (100.6 BPM) — test songs; local
                      only, git-ignored (copyrighted). Both need -half-beat (felt beat, user-verified)
out/                  generated maps (not source)
```

Pipeline: `audio.Decode` → `analysis.Analyze` → `mapper.NewTimeline` → `audio.EncodeOgg` with padding →
decode the Ogg and `MeasureOffset` against the original (re-encode once if > 2 ms off) →
`mapper.Generate` → `export.Build` → write → re-read → `synthfile.Check` + `rules.ValidateFile` (any
violation fails generation).

## Verified facts — do not break without re-testing in the editor

Confirmed by the user in Beatmap Editor 1.9.7 and on a Quest 3:
- Generated maps load with title/artist/cover; notes are on the editor's grid lines; bookmarks work.
  (This only showed grid alignment, not that the detected beat is the *felt* beat — see the half-beat
  gotcha.)
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
- **Beat phase can be half a beat off** (user's listening, 2026-11-21): on *both* test songs the felt beat
  is half a beat after the detected one, although the strongest onsets sit within ms of the detected
  beats. `analyze -beat-test` (low tock = detected beat, high tick = halfway) checks it by ear;
  `-half-beat` corrects it manually. Don't fix by always shifting — automatic detection needs a
  listening-verified set of songs. `-clicks` alone misleads: clicks on a drum hit get masked.
- Different MP3 decoders differ in start delay; that's why the export measures alignment instead of
  trusting it.
- The editor stores 32-bit floats (`0.00120002031`): compare positions with tolerances.
- Notes during the *other* hand's rail don't count toward same-hand runs (rules and mapper agree).
- Note selection is per bar (`selectByBars`): each bar's positions are ranked by metric weight + mean
  strength over all bars with a similar rhythm (cosine ≥ `RhythmSimilarity`), and a bar starts from the
  order of its most similar earlier bar (`RhythmTieMargin`). Hard groups (greedy clustering) and
  per-slot strength ranking both made repeated bars flip on near ties — don't go back to them.
- `similarity` sums in position order: map order made equal similarities differ in the last bit, so
  "most similar earlier bar" changed between runs (broke determinism).
- Onset `Tone` is brightness (flux-weighted log2 frequency), not pitch. On real music the same hit varies
  ≈ 0.2 octave bar to bar (≈ 1 cell of height), so repeats are decided per bar on the *mean* tone shift
  (`sameTone`, `RepeatToneOctaves`), not per note — per-note checks either jitter or never repeat.

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

Milestones 1–4 (format, analysis, first playable map, full mapper + validate + README) are done. Play
feedback (Quest 3, mostly Normal): density OK, but not "in touch" with the music. Step 1 done: per-bar
rhythm selection (repeated bars now get the same note rhythm; on the test song 12–17 of 20 repeating bars
vs 3–6 before). Step 2 done: positions follow the music — height from onset tone (rank correlation
tone↔height 0.6–0.84 on the test song, ≈ 0 before), move size from loudness (calm notes ≈ 1.3 cells from
home vs ≈ 1.8 in loud parts), repeated bars repeat positions within a phrase, motifs mirror sideways.
Play test (Normal, both songs): still "random". Cause found: the detected beat is half a beat off on
both songs (user-verified by ear), so step 1 favoured the real off-beats. `-half-beat` + `analyze
-beat-test` added. Re-played with `-half-beat`: **the rhythm is fine now** (user, 2026-11-21).

Next steps:
1. **Stem separation** (user's call): the rhythm works, but maps should follow more of what happens in
   the music (vocals, melody, which instrument leads). Plan: separate vocals/drums/bass/other with an
   optional external tool like sox (demucs.cpp is the Python-free candidate; ONNX via onnxruntime_go
   needs cgo), cache the stems next to the song, fall back to today's pipeline without it. Start with a
   one-off experiment on Pure Shores: notes from vocal-stem onsets + real vocal pitch.
2. Automatic beat phase (so `-half-beat` isn't needed): collect listening-verified songs with
   `analyze -beat-test` first; a drum stem from step 1 should make this much easier.
3. Smaller: section labels (Pure Shores: 27 of 28 phrases "calm" → sparse maps), density following
   energy continuously instead of 3 labels, more pairs (fire ~once per song).

Walls are opt-in (`-walls`, Hard+, calm phrases only). Older candidates:
- Fastest moves sit at the hand-speed limit on every difficulty (patterns get pulled in) → possibly
  smaller pattern offsets or lower density.
- Many rails on this song (Easy ≈ 12 % of notes) → `RailMinLevelDB` / `RailMinBeats`.
- Accent specials on Hard+ may be frequent (≤ 1 per bar).

Working style: the user plans each piece of work first, then expects implementation and a stop with
results plus concrete things to check (editor, Quest, listening).
