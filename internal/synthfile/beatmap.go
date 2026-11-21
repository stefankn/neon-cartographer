package synthfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// Difficulties lists the six difficulty keys in editor order.
var Difficulties = []string{"Easy", "Normal", "Hard", "Expert", "Master", "Custom"}

// Units and scales. See docs/synth-format.md.
const (
	TicksPerBeat = 64     // time keys: ticks per beat, independent of BPM
	ZPerSecond   = 20.0   // Position z = seconds * ZPerSecond
	GridScale    = 0.1365 // metres per editor grid cell
	GridXOffset  = 0.002  // editor snap offset added to x
	GridYOffset  = 0.0012 // editor snap offset added to y
)

// NoteType is the hand/color of a note.
type NoteType int

const (
	RightHanded      NoteType = 0
	LeftHanded       NoteType = 1
	OneHandSpecial   NoteType = 2
	BothHandsSpecial NoteType = 3
)

func (t NoteType) String() string {
	switch t {
	case RightHanded:
		return "RightHanded"
	case LeftHanded:
		return "LeftHanded"
	case OneHandSpecial:
		return "OneHandSpecial"
	case BothHandsSpecial:
		return "BothHandsSpecial"
	}
	return fmt.Sprintf("NoteType(%d)", int(t))
}

// Vec3 is a position [x, y, z] in metres (x, y) and z-time units.
type Vec3 [3]float64

// Note is a single orb, or a rail when Segments is non-nil.
type Note struct {
	ID        string   `json:"Id"`
	ComboID   int      `json:"ComboId"`
	Position  Vec3     `json:"Position"`
	Segments  []Vec3   `json:"Segments"` // nil => plain note (JSON null)
	Type      NoteType `json:"Type"`
	Direction int      `json:"Direction"`
	Extra     Extra    `json:"-"`
	present   fieldSet
}

type noteAlias Note

func (n *Note) UnmarshalJSON(b []byte) error {
	return unmarshalWithExtra(b, (*noteAlias)(n), &n.Extra, &n.present)
}
func (n Note) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(noteAlias(n), n.Extra, n.present)
}

// Wall is an obstacle. SlideType/ZRotation are absent for some wall lists.
type Wall struct {
	Time        float64  `json:"time"`
	SlideType   *int     `json:"slideType,omitempty"`
	Position    Vec3     `json:"position"`
	ZRotation   *float64 `json:"zRotation,omitempty"`
	Initialized bool     `json:"initialized"`
	Extra       Extra    `json:"-"`
	present     fieldSet
}

type wallAlias Wall

func (w *Wall) UnmarshalJSON(b []byte) error {
	return unmarshalWithExtra(b, (*wallAlias)(w), &w.Extra, &w.present)
}
func (w Wall) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(wallAlias(w), w.Extra, w.present)
}

// Bookmark marks a named position (time in ticks).
type Bookmark struct {
	Time    float64 `json:"time"`
	Name    string  `json:"name"`
	Extra   Extra   `json:"-"`
	present fieldSet
}

type bookmarkAlias Bookmark

func (bm *Bookmark) UnmarshalJSON(b []byte) error {
	return unmarshalWithExtra(b, (*bookmarkAlias)(bm), &bm.Extra, &bm.present)
}
func (bm Bookmark) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(bookmarkAlias(bm), bm.Extra, bm.present)
}

// Bookmarks wraps the bookmark list.
type Bookmarks struct {
	BookmarksList []Bookmark `json:"BookmarksList"`
}

// TickNotes maps a tick key (string, e.g. "128" or "21.333333333") to the
// notes starting at that tick. It marshals with keys in numeric order.
type TickNotes map[string][]Note

func (t TickNotes) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("null"), nil
	}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, ea := strconv.ParseFloat(keys[i], 64)
		b, eb := strconv.ParseFloat(keys[j], 64)
		if ea == nil && eb == nil && a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(t[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// TickKey formats a tick value as the editor does: integers without a
// decimal point, otherwise rounded to 11 decimals.
func TickKey(tick float64) string {
	if tick == math.Trunc(tick) {
		return strconv.FormatInt(int64(tick), 10)
	}
	r := math.Round(tick*1e11) / 1e11
	return strconv.FormatFloat(r, 'f', -1, 64)
}

// Beatmap is the content of beatmap.meta.bin.
type Beatmap struct {
	Name                         string                     `json:"Name"`
	Author                       string                     `json:"Author"` // the song's artist
	Artwork                      string                     `json:"Artwork"`
	ArtworkBytes                 *string                    `json:"ArtworkBytes"`
	AudioName                    string                     `json:"AudioName"`
	AudioData                    json.RawMessage            `json:"AudioData"`
	AudioFrecuency               int                        `json:"AudioFrecuency"` // sic
	AudioChannels                int                        `json:"AudioChannels"`
	BPM                          float64                    `json:"BPM"`
	Offset                       float64                    `json:"Offset"` // ms
	Track                        map[string]TickNotes       `json:"Track"`
	Effects                      map[string][]float64       `json:"Effects"`
	Bookmarks                    Bookmarks                  `json:"Bookmarks"`
	Jumps                        map[string]json.RawMessage `json:"Jumps"`
	Crouchs                      map[string][]Wall          `json:"Crouchs"`
	Slides                       map[string][]Wall          `json:"Slides"`
	Lights                       map[string][]float64       `json:"Lights"`
	Squares                      map[string][]Wall          `json:"Squares"`
	Triangles                    map[string][]Wall          `json:"Triangles"`
	DrumSamples                  json.RawMessage            `json:"DrumSamples"`
	FilePath                     string                     `json:"FilePath"`
	IsAdminOnly                  bool                       `json:"IsAdminOnly"`
	EditorVersion                string                     `json:"EditorVersion"`
	Beatmapper                   string                     `json:"Beatmapper"`
	CustomDifficultyName         string                     `json:"CustomDifficultyName"`
	CustomDifficultySpeed        float64                    `json:"CustomDifficultySpeed"`
	UsingBeatMeasure             bool                       `json:"UsingBeatMeasure"`
	UpdatedWithMovementPositions bool                       `json:"UpdatedWithMovementPositions"`
	ProductionMode               bool                       `json:"ProductionMode"`
	Tags                         []string                   `json:"Tags"`
	BeatConverted                bool                       `json:"BeatConverted"`
	BeatModified                 bool                       `json:"BeatModified"`
	ModifiedTime                 int64                      `json:"ModifiedTime"`
	Explicit                     bool                       `json:"Explicit"`
	Extra                        Extra                      `json:"-"`
	present                      fieldSet
}

type beatmapAlias Beatmap

func (bm *Beatmap) UnmarshalJSON(b []byte) error {
	return unmarshalWithExtra(b, (*beatmapAlias)(bm), &bm.Extra, &bm.present)
}
func (bm Beatmap) MarshalJSON() ([]byte, error) {
	a := beatmapAlias(bm)
	// RawMessage fields must be valid JSON when written.
	if len(a.AudioData) == 0 {
		a.AudioData = json.RawMessage("null")
	}
	if len(a.DrumSamples) == 0 {
		a.DrumSamples = json.RawMessage("null")
	}
	return marshalWithExtra(a, bm.Extra, bm.present)
}

// NewBeatmap returns a beatmap with every per-difficulty container
// initialized to empty values, as the editor writes it.
func NewBeatmap() *Beatmap {
	bm := &Beatmap{
		Artwork:                      "No cover",
		Track:                        map[string]TickNotes{},
		Effects:                      map[string][]float64{},
		Jumps:                        map[string]json.RawMessage{},
		Crouchs:                      map[string][]Wall{},
		Slides:                       map[string][]Wall{},
		Lights:                       map[string][]float64{},
		Squares:                      map[string][]Wall{},
		Triangles:                    map[string][]Wall{},
		Bookmarks:                    Bookmarks{BookmarksList: []Bookmark{}},
		FilePath:                     "Redacted",
		CustomDifficultyName:         "Custom",
		CustomDifficultySpeed:        1,
		UsingBeatMeasure:             true,
		UpdatedWithMovementPositions: true,
		Tags:                         []string{},
	}
	for _, d := range Difficulties {
		bm.Track[d] = TickNotes{}
		bm.Effects[d] = []float64{}
		bm.Jumps[d] = json.RawMessage("[]")
		bm.Crouchs[d] = []Wall{}
		bm.Slides[d] = []Wall{}
		bm.Lights[d] = []float64{}
		bm.Squares[d] = []Wall{}
		bm.Triangles[d] = []Wall{}
	}
	return bm
}

// BeatToSeconds converts a beat index to seconds at the map BPM.
func (bm *Beatmap) BeatToSeconds(beat float64) float64 { return beat * 60 / bm.BPM }

// SecondsToZ converts seconds to the Position z coordinate.
func SecondsToZ(s float64) float64 { return s * ZPerSecond }

// GridToMetres converts editor grid (x, y) to Position (x, y).
func GridToMetres(gx, gy float64) (float64, float64) {
	return gx*GridScale + GridXOffset, gy*GridScale + GridYOffset
}

// MetresToGrid converts Position (x, y) to editor grid coordinates.
func MetresToGrid(x, y float64) (float64, float64) {
	return (x - GridXOffset) / GridScale, (y - GridYOffset) / GridScale
}

// TrackData is the content of track.data.json.
type TrackData struct {
	Name                  string   `json:"name"`
	Artist                string   `json:"artist"`
	Duration              string   `json:"duration"` // "mm:ss"
	CoverImage            string   `json:"coverImage"`
	AudioFile             string   `json:"audioFile"`
	SupportedDifficulties []string `json:"supportedDifficulties"`
	BPM                   float64  `json:"bpm"`
	Mapper                string   `json:"mapper"`
	Extra                 Extra    `json:"-"`
	present               fieldSet
}

type trackDataAlias TrackData

func (td *TrackData) UnmarshalJSON(b []byte) error {
	return unmarshalWithExtra(b, (*trackDataAlias)(td), &td.Extra, &td.present)
}
func (td TrackData) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(trackDataAlias(td), td.Extra, td.present)
}

func sortStrings(s []string) { sort.Strings(s) }

// editor197Absent lists the top-level keys that Beatmap Editor 1.9.7 does
// not write (see reference/editor-sample.synth).
var editor197Absent = []string{"Squares", "Triangles", "BeatModified", "Explicit"}

// NewBeatmapEditor197 is NewBeatmap restricted to the key set the official
// Beatmap Editor 1.9.7 writes, which is proven to load in that editor.
func NewBeatmapEditor197() *Beatmap {
	bm := NewBeatmap()
	bm.present = fieldSet{}
	for k := range knownFields(reflect.TypeOf(beatmapAlias{})) {
		bm.present[k] = true
	}
	for _, k := range editor197Absent {
		delete(bm.present, k)
	}
	return bm
}
