# Optional shortcuts for development. Everything here is plain `go` underneath; see README.md.

song := "testdata/bonobo-drift.mp3"

# Optional stem separation tool (demucs): uv project in tools/stems (pyproject.toml + uv.lock, .venv ignored).
# NCART_DEMUCS points to another demucs executable.
demucs := env_var_or_default("NCART_DEMUCS", justfile_directory() / "tools/stems/.venv/bin/demucs")
# demucs device for `just stems`: auto = Apple GPU (mps, ≈ 55 s per 4½-min song on an M1 Pro) if torch can
# use it, else cpu (≈ 100 s). Sandboxed sessions (nono) can't reach Metal and get cpu.
# Override: just demucs_device=cpu stems …  (`stems` passes --shifts 0: demucs' default random shift makes
# every run differ; with 0, repeated runs on the same device are bit-identical, CPU and MPS)
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

# Separate a song into vocals/drums/bass/other: <song>.stems/htdemucs/<stem>.wav
stems song=song:
    @[ -f {{song}} ] || { echo "{{song}} not found"; exit 1; }
    @[ -x "{{demucs}}" ] || { echo "demucs not installed: run just setup-stems"; exit 1; }
    @device="{{demucs_device}}"; \
    if [ "$device" = auto ]; then \
        device=$("$(dirname "{{demucs}}")/python" -c "import torch; print('mps' if torch.backends.mps.is_available() else 'cpu')" 2>/dev/null || echo cpu); \
    fi; \
    echo "demucs on $device"; \
    "{{demucs}}" -n htdemucs -d "$device" --shifts 0 -o "{{song}}.stems" --filename "{stem}.{ext}" "{{song}}"

_song:
    @[ -f {{song}} ] || { echo "{{song}} not found: it isn't in the repo, put your own copy there (see README)"; exit 1; }
