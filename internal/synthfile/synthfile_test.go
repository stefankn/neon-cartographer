package synthfile

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const (
	editorSample = "../../reference/editor-sample.synth"
)

// buildFixtureArchive zips the fixture JSON the way the editor stores it.
func buildFixtureArchive(t *testing.T) []byte {
	t.Helper()
	bmJSON, err := os.ReadFile("testdata/fixture.beatmap.json")
	if err != nil {
		t.Fatal(err)
	}
	td := []byte(`{"name":"Fixture Song","artist":"Some Artist","duration":"03:00","coverImage":"cover.png",
"audioFile":"FixtureSong_0123abcd.ogg","supportedDifficulties":["","Normal","","","",""],"bpm":128.0,"mapper":"tester","extraMeta":true}`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct {
		name string
		data []byte
	}{
		{BeatmapEntry, append([]byte{0xEF, 0xBB, 0xBF}, bmJSON...)},
		{"FixtureSong_0123abcd.ogg", []byte("OggS fake audio \x00\x01\x02")},
		{"cover.png", []byte("\x89PNG fake")},
		{TrackDataEntry, encodeUTF16LEWithBOM(td)},
	} {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(e.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawEntries returns decoded (BOM/UTF-16 stripped) entry contents.
func rawEntries(t *testing.T, archive []byte) (names []string, data map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	data = map[string][]byte{}
	for _, zf := range zr.File {
		b, err := readZipFile(zf)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, zf.Name)
		data[zf.Name] = b
	}
	return names, data
}

func genericJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(decodeText(b), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return v
}

// assertSemanticRoundTrip decodes, re-encodes and compares the archive.
func assertSemanticRoundTrip(t *testing.T, in []byte) {
	t.Helper()
	f, err := Decode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	inNames, inData := rawEntries(t, in)
	outNames, outData := rawEntries(t, out)
	if !reflect.DeepEqual(inNames, outNames) {
		t.Fatalf("entry order changed:\n in=%v\nout=%v", inNames, outNames)
	}
	for _, name := range inNames {
		switch name {
		case BeatmapEntry, TrackDataEntry:
			a, b := genericJSON(t, inData[name]), genericJSON(t, outData[name])
			if !reflect.DeepEqual(a, b) {
				ja, _ := json.MarshalIndent(a, "", " ")
				jb, _ := json.MarshalIndent(b, "", " ")
				t.Fatalf("%s differs after round trip:\n--- in\n%s\n--- out\n%s", name, ja, jb)
			}
		default:
			if !bytes.Equal(inData[name], outData[name]) {
				t.Fatalf("binary entry %s changed", name)
			}
		}
	}
	// Re-encoding the decoded output must be stable.
	f2, err := Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := f2.Encode()
	if err != nil {
		t.Fatal(err)
	}
	_, outData2 := rawEntries(t, out2)
	for _, name := range []string{BeatmapEntry, TrackDataEntry} {
		if _, ok := outData[name]; !ok {
			continue
		}
		if !reflect.DeepEqual(genericJSON(t, outData[name]), genericJSON(t, outData2[name])) {
			t.Fatalf("%s not stable across a second round trip", name)
		}
	}
}

func TestRoundTripFixture(t *testing.T) {
	archive := buildFixtureArchive(t)
	assertSemanticRoundTrip(t, archive)

	f, err := Decode(archive)
	if err != nil {
		t.Fatal(err)
	}
	bm := f.Beatmap
	if bm.BPM != 128 || bm.Offset != 120 || bm.Author != "Some Artist" {
		t.Errorf("metadata: %+v", bm)
	}
	normal := bm.Track["Normal"]
	if got := len(normal["256"]); got != 2 {
		t.Fatalf("notes at tick 256: %d", got)
	}
	rail := normal["256"][0]
	if rail.Type != RightHanded || len(rail.Segments) != 3 {
		t.Errorf("rail: %+v", rail)
	}
	if normal["128"][0].Segments != nil {
		t.Error("plain note should have nil Segments")
	}
	if _, ok := bm.Extra["FutureTopLevelField"]; !ok {
		t.Error("unknown top-level field not preserved")
	}
	if _, ok := normal["128"][1].Extra["FutureNoteField"]; !ok {
		t.Error("unknown note field not preserved")
	}
	if w := bm.Slides["Normal"][0]; w.SlideType == nil || *w.SlideType != 3 || w.ZRotation == nil {
		t.Errorf("slide: %+v", w)
	}
	if w := bm.Crouchs["Normal"][0]; w.SlideType != nil || w.ZRotation != nil {
		t.Errorf("crouch should have no slideType/zRotation: %+v", w)
	}
	if f.TrackData == nil || f.TrackData.SupportedDifficulties[1] != "Normal" || f.TrackData.Extra["extraMeta"] == nil {
		t.Errorf("track data: %+v", f.TrackData)
	}
}

// TestRoundTripEditorSample checks a map saved by the official editor.
// Put it at reference/editor-sample.synth.
func TestRoundTripEditorSample(t *testing.T) {
	b, err := os.ReadFile(editorSample)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("reference/editor-sample.synth not present: save a map from the official editor there")
	}
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticRoundTrip(t, b)
}

// TestRoundTripAllUnencryptedReferences round-trips every other readable reference map.
func TestRoundTripAllUnencryptedReferences(t *testing.T) {
	paths, _ := filepath.Glob("../../reference/*.synth")
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(b); errors.Is(err, ErrEncrypted) {
			continue
		}
		t.Run(filepath.Base(p), func(t *testing.T) { assertSemanticRoundTrip(t, b) })
	}
}

// TestEncryptedMap mimics a synthriderz.com download: every entry except
// synthriderz.meta.json is stored with WinZip AES (method 99).
func TestEncryptedMap(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"song.ogg", BeatmapEntry, "cover.jpg", "synthriderz.meta.json", TrackDataEntry} {
		method := uint16(zipMethodAES)
		if name == "synthriderz.meta.json" {
			method = zip.Store
		}
		w, err := zw.CreateRaw(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("opaque"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "encrypted.synth")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := ListEntries(path)
	if err != nil || len(names) != 5 {
		t.Fatalf("ListEntries: %v %v", names, err)
	}
	if _, err := Read(path); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("want ErrEncrypted, got %v", err)
	}
}

func TestNewBeatmapWritesAllDifficulties(t *testing.T) {
	f := &File{Beatmap: NewBeatmap(), TrackData: &TrackData{SupportedDifficulties: make([]string, 6)}}
	f.Beatmap.BPM = 120
	f.Beatmap.Track["Hard"][TickKey(64)] = []Note{{ID: "Note_64RightHanded0", ComboID: -1, Type: RightHanded}}
	f.SetEntry("a.ogg", []byte("x"))
	out, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	names, data := rawEntries(t, out)
	if want := []string{BeatmapEntry, "a.ogg", TrackDataEntry}; !reflect.DeepEqual(names, want) {
		t.Fatalf("order %v, want %v", names, want)
	}
	if !bytes.HasPrefix(data[BeatmapEntry], []byte{0xEF, 0xBB, 0xBF}) || !bytes.Contains(data[BeatmapEntry], []byte("\r\n")) {
		t.Error("beatmap must be UTF-8 BOM + CRLF")
	}
	if !bytes.HasPrefix(data[TrackDataEntry], []byte{0xFF, 0xFE}) {
		t.Error("track data must be UTF-16LE BOM")
	}
	m := genericJSON(t, data[BeatmapEntry]).(map[string]any)
	for _, key := range []string{"Track", "Effects", "Jumps", "Crouchs", "Slides", "Lights", "Squares", "Triangles"} {
		if n := len(m[key].(map[string]any)); n != 6 {
			t.Errorf("%s has %d difficulties", key, n)
		}
	}
	if m["Track"].(map[string]any)["Easy"] == nil || m["AudioData"] != nil {
		t.Error("empty difficulty should be {} and AudioData null")
	}
	note := m["Track"].(map[string]any)["Hard"].(map[string]any)["64"].([]any)[0].(map[string]any)
	if v, ok := note["Segments"]; !ok || v != nil {
		t.Error("plain note must serialize Segments: null")
	}
}

func TestTickKey(t *testing.T) {
	cases := map[float64]string{0: "0", 64: "64", 128: "128", 64.0 / 3: "21.33333333333", 96: "96", 32.5: "32.5"}
	for in, want := range cases {
		if got := TickKey(in); got != want {
			t.Errorf("TickKey(%v)=%q want %q", in, got, want)
		}
	}
}

func TestTickNotesNumericOrder(t *testing.T) {
	b, _ := json.Marshal(TickNotes{"1024": {}, "128": {}, "21.5": {}})
	if string(b) != `{"21.5":[],"128":[],"1024":[]}` {
		t.Fatalf("got %s", b)
	}
}

func TestGridConversion(t *testing.T) {
	x, y := GridToMetres(3, -2)
	gx, gy := MetresToGrid(x, y)
	if d := gx - 3 + gy + 2; d > 1e-9 || d < -1e-9 {
		t.Fatalf("grid round trip: %v %v", gx, gy)
	}
}

// TestCheckEditorSample checks our unit model against real editor output:
// tick keys, z coordinates and grid positions must agree.
func TestCheckEditorSample(t *testing.T) {
	f, err := Read(editorSample)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("reference/editor-sample.synth not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	if probs := Check(f); len(probs) > 0 {
		t.Fatalf("editor sample fails Check: %v", probs)
	}
}

func TestCheckFindsProblems(t *testing.T) {
	newFile := func() *File {
		f := &File{Beatmap: NewBeatmap()}
		f.Beatmap.BPM = 120
		f.Beatmap.AudioName = "a.ogg"
		f.SetEntry("a.ogg", []byte("x"))
		x, y := GridToMetres(3, 0)
		// tick 128 = beat 2 = 1 s at 120 BPM → z = 20
		f.Beatmap.Track["Normal"]["128"] = []Note{{ID: "n", Type: RightHanded, Position: Vec3{x, y, 20}}}
		return f
	}
	if p := Check(newFile()); len(p) != 0 {
		t.Fatalf("valid map reported problems: %v", p)
	}
	cases := map[string]func(f *File){
		"missing audio": func(f *File) { f.Beatmap.AudioName = "b.ogg" },
		"z mismatch":    func(f *File) { f.Beatmap.Track["Normal"]["128"][0].Position[2] = 21 },
		"off grid":      func(f *File) { f.Beatmap.Track["Normal"]["128"][0].Position[0] += 0.05 },
		"duplicate type": func(f *File) {
			n := f.Beatmap.Track["Normal"]["128"][0]
			f.Beatmap.Track["Normal"]["128"] = append(f.Beatmap.Track["Normal"]["128"], n)
		},
		"bad rail": func(f *File) {
			f.Beatmap.Track["Normal"]["128"][0].Segments = []Vec3{{0, 0, 19}}
		},
		"bad key":       func(f *File) { f.Beatmap.Track["Normal"]["x"] = []Note{{}} },
		"unknown diff":  func(f *File) { f.Beatmap.Track["Insane"] = TickNotes{} },
		"wall z":        func(f *File) { f.Beatmap.Slides["Normal"] = []Wall{{Time: 64, Position: Vec3{0, 0, 3}}} },
		"bad note type": func(f *File) { f.Beatmap.Track["Normal"]["128"][0].Type = 7 },
	}
	for name, mutate := range cases {
		f := newFile()
		mutate(f)
		if p := Check(f); len(p) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
}

func TestEditor197KeySet(t *testing.T) {
	f, err := Read(editorSample)
	if err != nil {
		t.Skip("reference/editor-sample.synth not present")
	}
	keys := func(bm *Beatmap) []string {
		b, err := json.Marshal(bm)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		var k []string
		for key := range m {
			k = append(k, key)
		}
		sort.Strings(k)
		return k
	}
	if got, want := keys(NewBeatmapEditor197()), keys(f.Beatmap); !reflect.DeepEqual(got, want) {
		t.Fatalf("NewBeatmapEditor197 keys\n got %v\nwant %v", got, want)
	}
}
