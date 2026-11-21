package synthfile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Extra holds JSON object members that have no typed Go field, so data
// written by newer editor versions survives a read/write round trip.
type Extra map[string]json.RawMessage

// fieldSet records which typed members were present in the decoded input.
// Editor versions differ in which keys they write (1.9.7 has no "Squares",
// "Triangles", "BeatModified" or "Explicit"), so on re-encode we write only
// the keys that were there. A nil set (newly built value) writes every field.
type fieldSet map[string]bool

var knownFieldsCache sync.Map // reflect.Type -> map[string]bool

// knownFields returns the JSON names of a struct's typed fields.
func knownFields(t reflect.Type) map[string]bool {
	if v, ok := knownFieldsCache.Load(t); ok {
		return v.(map[string]bool)
	}
	m := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		m[name] = true
	}
	knownFieldsCache.Store(t, m)
	return m
}

// unmarshalWithExtra decodes data into v (a pointer to a struct alias with no
// custom methods), puts unknown members into *extra and records which known
// members were present in *present.
func unmarshalWithExtra(data []byte, v any, extra *Extra, present *fieldSet) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	known := knownFields(reflect.TypeOf(v).Elem())
	seen := fieldSet{}
	for k, raw := range all {
		if known[k] {
			seen[k] = true
			delete(all, k)
			continue
		}
		// Compact so the stored value doesn't depend on input whitespace.
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return err
		}
		all[k] = buf.Bytes()
	}
	*present = seen
	if len(all) > 0 {
		*extra = all
	} else {
		*extra = nil
	}
	return nil
}

// marshalWithExtra encodes v, drops typed members absent from present (when
// present is non-nil) and merges in extra members (typed fields win).
func marshalWithExtra(v any, extra Extra, present fieldSet) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || (len(extra) == 0 && present == nil) {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if present != nil {
		for k := range m {
			if !present[k] {
				delete(m, k)
			}
		}
	}
	for k, raw := range extra {
		if _, ok := m[k]; !ok {
			m[k] = raw
		}
	}
	return marshalOrdered(m, reflect.TypeOf(v))
}

// marshalOrdered writes m with typed fields first in struct order, then the
// extra keys sorted, so output stays close to the editor's layout.
func marshalOrdered(m map[string]json.RawMessage, t reflect.Type) ([]byte, error) {
	var keys []string
	used := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := m[name]; ok && !used[name] {
			keys = append(keys, name)
			used[name] = true
		}
	}
	var rest []string
	for k := range m {
		if !used[k] {
			rest = append(rest, k)
		}
	}
	sortStrings(rest)
	keys = append(keys, rest...)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(m[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
