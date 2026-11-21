// Package export turns a mapper.Map into a .synth file that mirrors what
// Beatmap Editor 1.9.7 writes.
package export

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/stefankn/neon-cartographer/internal/mapper"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

// Version is written to the beatmap's EditorVersion field.
const Version = "neon-cartographer 0.3"

// Meta is song metadata for the map.
type Meta struct {
	Title, Artist, Mapper string
	Duration              float64 // seconds of the exported audio
	SampleRate, Channels  int     // of the exported audio
}

// AudioFileName returns "<title≤16 identifier chars>_<sha256[:8]>.ogg"; the
// hash changes the name whenever the audio changes, because the editor caches
// spectrum data by file name.
func AudioFileName(title string, ogg []byte) string {
	var b strings.Builder
	for _, r := range title {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		name = "song"
	}
	if len(name) > 16 {
		name = name[:16]
	}
	sum := sha256.Sum256(ogg)
	return fmt.Sprintf("%s_%s.ogg", name, hex.EncodeToString(sum[:4]))
}

// Build assembles the .synth file. cover may be nil (PNG bytes otherwise).
func Build(m *mapper.Map, meta Meta, ogg, cover []byte) (*synthfile.File, error) {
	if m.BPM <= 0 {
		return nil, fmt.Errorf("export: BPM must be > 0")
	}
	bpm := math.Round(m.BPM*100) / 100
	period := 60 / bpm
	bm := synthfile.NewBeatmapEditor197()
	bm.Name, bm.Author, bm.Beatmapper = meta.Title, meta.Artist, meta.Mapper
	bm.AudioName = AudioFileName(meta.Title, ogg)
	bm.AudioFrecuency, bm.AudioChannels = meta.SampleRate, meta.Channels
	bm.BPM = bpm
	bm.Offset = 0
	bm.FilePath = ""
	bm.EditorVersion = Version
	bm.ModifiedTime = time.Now().Unix()
	bm.Artwork = "Default Image"
	if len(cover) > 0 {
		s := base64.StdEncoding.EncodeToString(cover)
		bm.Artwork, bm.ArtworkBytes = "cover.png", &s
	}

	position := func(beat, x, y float64) synthfile.Vec3 {
		mx, my := synthfile.GridToMetres(x, y)
		return synthfile.Vec3{round9(mx), round9(my), round9(synthfile.SecondsToZ(beat * period))}
	}
	supported := make([]string, len(synthfile.Difficulties))
	for _, d := range m.Difficulties {
		ticks := bm.Track[d.Name]
		if ticks == nil {
			return nil, fmt.Errorf("export: unknown difficulty %q", d.Name)
		}
		notes := append([]mapper.Note(nil), d.Notes...)
		sort.SliceStable(notes, func(i, j int) bool { return notes[i].Beat < notes[j].Beat })
		for i, n := range notes {
			tick := n.Beat * synthfile.TicksPerBeat
			key := synthfile.TickKey(math.Round(tick*1e6) / 1e6)
			sn := synthfile.Note{
				ID:       fmt.Sprintf("Note_%s%s%d", key, n.Type, i+1),
				ComboID:  -1,
				Position: position(n.Beat, n.X, n.Y),
				Type:     n.Type,
			}
			for _, rp := range n.Rail {
				sn.Segments = append(sn.Segments, position(rp.Beat, rp.X, rp.Y))
			}
			for _, existing := range ticks[key] {
				if existing.Type == n.Type {
					return nil, fmt.Errorf("export: %s has two %v notes at beat %v", d.Name, n.Type, n.Beat)
				}
			}
			ticks[key] = append(ticks[key], sn)
		}
		for _, w := range d.Walls {
			st, rot := w.SlideType, 0.0
			bm.Slides[d.Name] = append(bm.Slides[d.Name], synthfile.Wall{
				Time:      math.Round(w.Beat * synthfile.TicksPerBeat),
				SlideType: &st,
				Position:  synthfile.Vec3{0, 0, round9(synthfile.SecondsToZ(w.Beat * period))},
				ZRotation: &rot, Initialized: true,
			})
		}
		if len(notes) > 0 {
			for i, name := range synthfile.Difficulties {
				if name == d.Name {
					supported[i] = name
				}
			}
		}
	}
	for _, b := range m.Bookmarks {
		bm.Bookmarks.BookmarksList = append(bm.Bookmarks.BookmarksList,
			synthfile.Bookmark{Time: math.Round(b.Beat * synthfile.TicksPerBeat), Name: b.Name})
	}

	f := &synthfile.File{Beatmap: bm}
	f.SetEntry(bm.AudioName, ogg)
	coverName := "Default Image"
	if len(cover) > 0 {
		coverName = "cover.png"
		f.SetEntry(coverName, cover)
	}
	secs := int(math.Round(meta.Duration))
	f.TrackData = &synthfile.TrackData{
		Name: meta.Title, Artist: meta.Artist, Duration: fmt.Sprintf("%02d:%02d", secs/60, secs%60),
		CoverImage: coverName, AudioFile: bm.AudioName, SupportedDifficulties: supported,
		BPM: bpm, Mapper: meta.Mapper,
	}
	return f, nil
}

func round9(x float64) float64 { return math.Round(x*1e9) / 1e9 }
