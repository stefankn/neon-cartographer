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
Optional, experimental: **demucs** via `just setup-stems` (needs uv; sandboxed sessions need write access
to `~/.local/share/uv` plus outgoing network for uv *and* the venv's Python, which downloads the model on
first run). `just stems` picks the device itself (`demucs_device=auto`: MPS if torch can use it, else
CPU). **In a nono-sandboxed session Metal is unreachable** (nono 0.81 has no GPU/IOKit grant; torch then
says "MPS backend is supported on MacOS 13.0+", which is misleading — outside the sandbox MPS works), so
Claude gets the CPU (≈ 100 s per song) automatically; don't force `mps` there. Stems are cached per song,
so this only matters for new songs.

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
                      only, git-ignored (copyrighted). Both need -half-beat (felt beat, user-verified).
                      <song>.stems/ = demucs output (git-ignored, copyrighted too)
tools/stems/          uv project for the optional demucs tool (pyproject.toml, uv.lock; .venv git-ignored)
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
- **demucs is random by default** (`--shifts 1` = one random time shift): two CPU runs on the same clip
  differed by 8 % RMS on vocals; GPU vs CPU stems of Bonobo by 4–18 %. `just stems` passes `--shifts 0`
  → repeated runs are bit-identical on the same device (CPU: clip; MPS: full Bonobo, checked by the user
  2026-11-21). GPU and CPU still differ from each other, so treat a
  song's cached stems as fixed input. Bonobo's stems sum to the original within 10 % (Pure Shores 2.3 %) on
  both devices — that's the song, not the device.
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
   the music (vocals, melody, which instrument leads). Tool: Python **demucs** (htdemucs) in a uv venv
   (user's call, over demucs.cpp/ONNX). **Done:** uv project `tools/stems` (pyproject.toml + committed
   uv.lock: demucs 4.0.1 / torch+torchaudio 2.8 / soundfile — torchaudio 2.8 has no save backend without
   soundfile); `just setup-stems` = `uv sync --locked` into `tools/stems/.venv` (uv-managed Python 3.12 in
   `~/.local/share/uv/python`; `NCART_DEMUCS` overrides the demucs path — step 3's Go side should use the
   same default and override) and `just stems <song>` → `<song>.stems/htdemucs/<stem>.wav` (MPS ≈ 55 s if
   available, else CPU ≈ 100 s per song; `--shifts 0` for reproducible stems). Measured on Pure Shores: stems are sample-exact with the *ffmpeg* decode (lag 0,
   stem sum within 2.3 % RMS); our native MP3 decoder and sox/MAD start ≈ 50 ms later → stems must be
   aligned with `MeasureOffset`, never assumed. Next: Go side (`internal/stems`: find cache, align,
   fall back without stems), then the vocal experiment: notes from vocal-stem onsets + real vocal pitch.
   **Decided (user, 2026-11-21):** when stems are used, ncart decodes the song through ffmpeg (the existing
   fallback path in `audio/decode.go`) so analysis and stems share one clock; still run `MeasureOffset`
   stems ↔ analysed audio as a check (expect ≈ 0 ms). Not chosen: feeding demucs our own decode (it's
   mono; demucs separates better from stereo).
2. Automatic beat phase (so `-half-beat` isn't needed): **the drum stem's detected beat equals the
   user-verified felt beat on both test songs** (Pure Shores 0.447 s vs `-half-beat` 0.450 s; Bonobo 0.134
   vs 0.137, on the ffmpeg clock). Bass stem agrees on Pure Shores only. Two songs is not a verified set —
   still collect more listening-verified songs with `analyze -beat-test` before relying on it.
3. Smaller: section labels (Pure Shores: 27 of 28 phrases "calm" → sparse maps), density following
   energy continuously instead of 3 labels, more pairs (fire ~once per song).

Walls are opt-in (`-walls`, Hard+, calm phrases only). Older candidates:
- Fastest moves sit at the hand-speed limit on every difficulty (patterns get pulled in) → possibly
  smaller pattern offsets or lower density.
- Many rails on this song (Easy ≈ 12 % of notes) → `RailMinLevelDB` / `RailMinBeats`.
- Accent specials on Hard+ may be frequent (≤ 1 per bar).

Working style: the user plans each piece of work first, then expects implementation and a stop with
results plus concrete things to check (editor, Quest, listening).
