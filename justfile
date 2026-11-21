# Optional shortcuts for development. Everything here is plain `go` underneath; see README.md.

song := "testdata/bonobo-drift.mp3"

# Optional stem separation tool (demucs): uv project in tools/stems (pyproject.toml + uv.lock, .venv ignored).
# ncart finds it there (or via NCART_DEMUCS) and runs it itself; see internal/stems.
demucs := env_var_or_default("NCART_DEMUCS", justfile_directory() / "tools/stems/.venv/bin/demucs")
# demucs device for `just stems`: auto = Apple GPU (mps, ≈ 55 s per 4½-min song on an M1 Pro) if torch can
# use it, else cpu (≈ 100 s). Sandboxed sessions (nono) can't reach Metal and get cpu.
# Override: just demucs_device=cpu stems …
demucs_device := "auto"

# List the recipes
default:
    @just --list

# Build the ncart binary into ./ncart
build:
    go build -o ncart ./cmd/ncart

# Run all tests; extra go test flags pass through, e.g. `just test -run Tone -v`
test *args:
    go test ./... {{args}}

# vet, formatting (fails if any file needs gofmt) and all tests — run before committing
check:
    go vet ./...
    @files="$(gofmt -l .)"; if [ -n "$files" ]; then echo "gofmt needed:"; echo "$files"; exit 1; fi
    go test ./...

# Generate a map for the test song (all difficulties, walls) and validate it
demo: _song
    @mkdir -p out
    go run ./cmd/ncart -in {{song}} -out out/demo.synth -difficulties easy,normal,hard,expert,master -walls
    go run ./cmd/ncart validate out/demo.synth

# Analyse the test song: summary, out/analysis.json and a click track out/check.wav to listen to
analyze: _song
    @mkdir -p out
    go run ./cmd/ncart analyze -in {{song}} -debug out/analysis.json -clicks out/check.wav

# uv-managed Python 3.12; system Python untouched. Change versions in tools/stems/pyproject.toml, then
# `uv lock --project tools/stems`. Remove: rm -rf tools/stems/.venv; optionally `uv python uninstall 3.12`
# Create/update the demucs venv tools/stems/.venv exactly as locked in uv.lock
setup-stems:
    uv sync --project tools/stems --managed-python --locked
    "{{demucs}}" --help >/dev/null && echo "demucs ready: {{demucs}}"

# Separate a song into vocals/drums/bass/other (skipped if cached; `just stems <song> -force` redoes it)
stems song=song *flags:
    go run ./cmd/ncart stems -in "{{song}}" -device {{demucs_device}} {{flags}}

_song:
    @[ -f {{song}} ] || { echo "{{song}} not found: it isn't in the repo, put your own copy there (see README)"; exit 1; }
