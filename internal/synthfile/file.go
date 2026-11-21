// Package synthfile reads and writes Synth Riders .synth map files.
// The format is documented in docs/synth-format.md.
package synthfile

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf16"
)

const (
	BeatmapEntry   = "beatmap.meta.bin"
	TrackDataEntry = "track.data.json"
	zipMethodAES   = 99
)

// ErrEncrypted is returned for maps whose entries are AES-encrypted
// (official or synthriderz.com downloads). They cannot be read.
var ErrEncrypted = errors.New("synthfile: map entries are AES-encrypted")

// Entry is a raw zip entry other than the two JSON files (audio, cover, ...).
type Entry struct {
	Name string
	Data []byte
}

// File is a whole .synth archive.
type File struct {
	Beatmap   *Beatmap
	TrackData *TrackData // nil if the archive has none
	Entries   []Entry    // other entries, in archive order
	// order lists every entry name in archive order, so writing keeps the
	// original layout. Built automatically when empty.
	order []string
}

// Entry returns the named raw entry, or nil.
func (f *File) Entry(name string) *Entry {
	for i := range f.Entries {
		if f.Entries[i].Name == name {
			return &f.Entries[i]
		}
	}
	return nil
}

// SetEntry adds or replaces a raw entry.
func (f *File) SetEntry(name string, data []byte) {
	if e := f.Entry(name); e != nil {
		e.Data = data
		return
	}
	f.Entries = append(f.Entries, Entry{Name: name, Data: data})
}

// ListEntries returns the entry names of a zip without decoding them.
func ListEntries(path string) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	names := make([]string, len(zr.File))
	for i, zf := range zr.File {
		names[i] = zf.Name
	}
	return names, nil
}

// Read loads a .synth file from disk.
func Read(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(b)
}

// Decode parses a .synth archive held in memory.
func Decode(b []byte) (*File, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("synthfile: not a zip archive: %w", err)
	}
	f := &File{}
	for _, zf := range zr.File {
		if zf.Method == zipMethodAES && (zf.Name == BeatmapEntry || zf.Name == TrackDataEntry) {
			return nil, ErrEncrypted
		}
		data, err := readZipFile(zf)
		if err != nil {
			return nil, fmt.Errorf("synthfile: entry %s: %w", zf.Name, err)
		}
		f.order = append(f.order, zf.Name)
		switch zf.Name {
		case BeatmapEntry:
			bm := &Beatmap{}
			if err := json.Unmarshal(decodeText(data), bm); err != nil {
				return nil, fmt.Errorf("synthfile: %s: %w", BeatmapEntry, err)
			}
			f.Beatmap = bm
		case TrackDataEntry:
			td := &TrackData{}
			if err := json.Unmarshal(decodeText(data), td); err != nil {
				return nil, fmt.Errorf("synthfile: %s: %w", TrackDataEntry, err)
			}
			f.TrackData = td
		default:
			f.Entries = append(f.Entries, Entry{Name: zf.Name, Data: data})
		}
	}
	if f.Beatmap == nil {
		return nil, fmt.Errorf("synthfile: missing %s", BeatmapEntry)
	}
	return f, nil
}

func readZipFile(zf *zip.File) ([]byte, error) {
	if zf.Method == zipMethodAES {
		return nil, ErrEncrypted
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// decodeText converts UTF-8 (optional BOM) or UTF-16 (with BOM) to UTF-8.
func decodeText(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return utf16ToUTF8(b[2:], binary.LittleEndian)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return utf16ToUTF8(b[2:], binary.BigEndian)
	}
	return b
}

func utf16ToUTF8(b []byte, order binary.ByteOrder) []byte {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = order.Uint16(b[2*i:])
	}
	return []byte(string(utf16.Decode(u)))
}

func encodeUTF16LEWithBOM(s []byte) []byte {
	u := utf16.Encode([]rune(string(s)))
	out := make([]byte, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for i, c := range u {
		binary.LittleEndian.PutUint16(out[2+2*i:], c)
	}
	return out
}

// EncodeBeatmapJSON renders beatmap.meta.bin as the editor does:
// UTF-8 with BOM, 2-space indent, CRLF line endings.
func EncodeBeatmapJSON(bm *Beatmap) ([]byte, error) {
	raw, err := json.Marshal(bm)
	if err != nil {
		return nil, err
	}
	var ind bytes.Buffer
	if err := json.Indent(&ind, raw, "", "  "); err != nil {
		return nil, err
	}
	out := append([]byte{0xEF, 0xBB, 0xBF}, bytes.ReplaceAll(ind.Bytes(), []byte("\n"), []byte("\r\n"))...)
	return out, nil
}

// EncodeTrackDataJSON renders track.data.json: UTF-16LE with BOM, 4-space indent.
func EncodeTrackDataJSON(td *TrackData) ([]byte, error) {
	raw, err := json.Marshal(td)
	if err != nil {
		return nil, err
	}
	var ind bytes.Buffer
	if err := json.Indent(&ind, raw, "", "    "); err != nil {
		return nil, err
	}
	return encodeUTF16LEWithBOM(ind.Bytes()), nil
}

// Encode serializes the archive.
func (f *File) Encode() ([]byte, error) {
	order := f.entryOrder()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now()
	for _, name := range order {
		var data []byte
		var err error
		switch name {
		case BeatmapEntry:
			data, err = EncodeBeatmapJSON(f.Beatmap)
		case TrackDataEntry:
			data, err = EncodeTrackDataJSON(f.TrackData)
		default:
			e := f.Entry(name)
			if e == nil {
				continue
			}
			data = e.Data
		}
		if err != nil {
			return nil, fmt.Errorf("synthfile: encoding %s: %w", name, err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// entryOrder returns the write order: the original order for entries that
// still exist, then any new entries. Beatmap first, track data last for new files.
func (f *File) entryOrder() []string {
	want := map[string]bool{BeatmapEntry: f.Beatmap != nil, TrackDataEntry: f.TrackData != nil}
	for _, e := range f.Entries {
		want[e.Name] = true
	}
	var order []string
	seen := map[string]bool{}
	add := func(n string) {
		if want[n] && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	for _, n := range f.order {
		add(n)
	}
	add(BeatmapEntry)
	for _, e := range f.Entries {
		add(e.Name)
	}
	add(TrackDataEntry)
	return order
}

// Write saves the archive to path (written to a temp file, then renamed).
func (f *File) Write(path string) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
