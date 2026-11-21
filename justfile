# Optional shortcuts for development. Everything here is plain `go` underneath; see README.md.

song := "testdata/bonobo-drift.mp3"

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

_song:
    @[ -f {{song}} ] || { echo "{{song}} not found: it isn't in the repo, put your own copy there (see README)"; exit 1; }
