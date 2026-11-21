package export

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/stefankn/neon-cartographer/internal/mapper"
	"github.com/stefankn/neon-cartographer/internal/synthfile"
)

func sampleMap() *mapper.Map {
	return &mapper.Map{
		BPM: 126,
		Difficulties: []mapper.Difficulty{
			{Name: "Normal", Notes: []mapper.Note{
				{Beat: 5, Type: synthfile.RightHanded, X: 3, Y: 0},
				{Beat: 6, Type: synthfile.LeftHanded, X: -3, Y: 1},
				{Beat: 7.5, Type: synthfile.RightHanded, X: 3, Y: -1, Rail: []mapper.RailPoint{{Beat: 9, X: 2, Y: 0}}},
			}},
			{Name: "Easy"},
		},
		Bookmarks: []mapper.Bookmark{{Beat: 5, Name: "intense"}},
	}
}

func build(t *testing.T, cover []byte) *synthfile.File {
	t.Helper()
	f, err := Build(sampleMap(), Meta{Title: "Drift!", Artist: "Bonobo", Mapper: "neon-cartographer", Duration: 270.4, SampleRate: 44100, Channels: 2},
		[]byte("OggS..."), cover)
	if err != nil {
		t.Fatal(err)
	}
	// encode and decode like a real write
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := synthfile.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return f2
}

func TestBuildPassesCheck(t *testing.T) {
	f := build(t, []byte("\x89PNG fake"))
	if p := synthfile.Check(f); len(p) > 0 {
		t.Fatalf("Check: %v", p)
	}
	bm := f.Beatmap
	if bm.BPM != 126 || bm.Offset != 0 || bm.Author != "Bonobo" || bm.Name != "Drift!" {
		t.Errorf("meta %+v", bm)
	}
	if !regexp.MustCompile(`^Drift_[0-9a-f]{8}\.ogg$`).MatchString(bm.AudioName) {
		t.Errorf("audio name %q", bm.AudioName)
	}
	normal := bm.Track["Normal"]
	// beat 5 → tick 320; 1 s = 126/60 beats → z = beat*60/126*20
	n := normal["320"]
	if len(n) != 1 || n[0].ID != "Note_320RightHanded1" || n[0].ComboID != -1 || n[0].Segments != nil {
		t.Fatalf("tick 320: %+v", n)
	}
	if rail := normal["480"]; len(rail) != 1 || len(rail[0].Segments) != 1 || rail[0].ID != "Note_480RightHanded3" {
		t.Fatalf("rail at tick 480: %+v", rail)
	}
	td := f.TrackData
	if td.Duration != "04:30" || td.AudioFile != bm.AudioName || td.CoverImage != "cover.png" ||
		!reflect.DeepEqual(td.SupportedDifficulties, []string{"", "Normal", "", "", "", ""}) {
		t.Errorf("track data %+v", td)
	}
	if f.Entry("cover.png") == nil || bm.ArtworkBytes == nil {
		t.Error("cover missing")
	}
	if len(bm.Bookmarks.BookmarksList) != 1 || bm.Bookmarks.BookmarksList[0].Time != 320 {
		t.Errorf("bookmarks %+v", bm.Bookmarks)
	}
}

func TestBuildWithoutCover(t *testing.T) {
	f := build(t, nil)
	if f.Beatmap.Artwork != "Default Image" || f.Beatmap.ArtworkBytes != nil || f.Entry("cover.png") != nil {
		t.Errorf("artwork %q bytes %v", f.Beatmap.Artwork, f.Beatmap.ArtworkBytes)
	}
}

// The written key set must equal the one Beatmap Editor 1.9.7 wrote.
func TestBuildMatchesEditorKeySet(t *testing.T) {
	sample, err := synthfile.Read("../../reference/editor-sample.synth")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("editor sample not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	keys := func(bm *synthfile.Beatmap) []string {
		b, _ := json.Marshal(bm)
		var m map[string]any
		json.Unmarshal(b, &m)
		var k []string
		for key := range m {
			k = append(k, key)
		}
		sort.Strings(k)
		return k
	}
	if got, want := keys(build(t, nil).Beatmap), keys(sample.Beatmap); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys\n got %v\nwant %v", got, want)
	}
}

func TestDuplicateTypeRejected(t *testing.T) {
	m := sampleMap()
	m.Difficulties[0].Notes = append(m.Difficulties[0].Notes, mapper.Note{Beat: 5, Type: synthfile.RightHanded, X: 1})
	if _, err := Build(m, Meta{Title: "x"}, []byte("o"), nil); err == nil {
		t.Fatal("two right-hand notes on one tick accepted")
	}
}

func TestWallsWrittenLikeEditor(t *testing.T) {
	m := sampleMap()
	m.Difficulties[0].Walls = []mapper.Wall{{Beat: 12, SlideType: 1}}
	f, err := Build(m, Meta{Title: "x"}, []byte("o"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(f.Beatmap.Slides["Normal"])
	want := `[{"time":768,"slideType":1,"position":[0,0,114.285714286],"zRotation":0,"initialized":true}]`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if p := synthfile.Check(f); len(p) > 0 {
		t.Fatalf("Check: %v", p)
	}
}
