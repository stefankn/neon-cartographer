# The `.synth` file format

Status: written from MIT-licensed [Synth Mapping Helper](https://github.com/adosikas/synth_mapping_helper)
v1.5.24 (`synth_format.py`), the encrypted download `eiffel_65-blue.synth`, and **confirmed against
`reference/editor-sample.synth`** (saved by official Beatmap Editor 1.9.7-10: 16 notes incl. 7 rails across
Easy/Normal, all note types, all 5 slide wall types, crouch walls, effects). The tests in
`internal/synthfile` check these rules against that file. Items still marked **(verify)** were not covered
by the sample.

**The sample's audio has been replaced.** The song (copyrighted) was swapped for 1 s of silent stereo
44.1 kHz Ogg Vorbis under the same entry name, `PureShores.ogg`. `beatmap.meta.bin` and
`track.data.json` are still byte-for-byte what the editor wrote. The zip container itself was rebuilt by
a script (same entry order, names, timestamps, deflate). The editor's original container had entry order
`beatmap.meta.bin`, `PureShores.ogg`, `track.data.json`, all deflated, MS-DOS/NT origin, a 36-byte extra
field per central-directory entry, and no comments. So treat the JSON as ground truth, not the zip
layout. If a test ever needs audio, save a new map in the editor using your own audio.

## Container

A `.synth` file is a standard ZIP archive (deflate). Entries:

| Entry | Content |
|---|---|
| `beatmap.meta.bin` | The beatmap. **JSON**, despite the extension. UTF-8 **with BOM**, CRLF line endings, 2-space indent. |
| `track.data.json` | Short song-select metadata. JSON encoded **UTF-16LE with BOM**, 4-space indent. |

Editor 1.9.7 writes the entries in the order `beatmap.meta.bin`, audio, `track.data.json`; the sample has
no cover entry (`Artwork: "Default Image"`, `ArtworkBytes: null`).
| `<audio>.ogg` | The song. Must be **Ogg Vorbis** (the editor rejects other formats). The name is set by `AudioName`. |
| `<cover>.png/.jpg` | Optional cover image (`Artwork`). Its bytes are also stored base64 in `ArtworkBytes`. |
| `synthriderz.meta.json` | Only in maps downloaded from synthriderz.com: `{"id":350,"hash":"<sha256>"}`. Not needed. |

The editor's own files also carry NTFS timestamps (extra field 0x000A) and the UTF-8 filename flag.
These details are optional.

**Encrypted maps:** the reference download `eiffel_65-blue.synth` stores every entry except
`synthriderz.meta.json` as **WinZip AES** (method 99). Official/"production" maps use this; we can't read
them and don't need to. Custom maps saved by the editor are unencrypted. `synthfile.Read` returns
`ErrEncrypted` for these.

Audio file naming: the editor caches spectrum data by file name. Synth Mapping Helper uses
`<name≤16 identifier chars>_<sha256[:8]>.ogg` so the name changes when the audio changes. We do the same.

## Units and time

* **BPM**: `BPM` (float). One tempo per map; the helper also treats it as global.
* **Ticks**: 64 ticks per beat, independent of BPM. Used for note dictionary keys, wall `time`, `Lights`,
  `Effects` and bookmarks. Keys are strings: integral values are written as integers (`"128"`), otherwise
  as floats rounded to the nearest 1/64 or 1/48 beat (`"21.33333333333"`, 11 decimals).
* **Z coordinate** in `Position`: `z = seconds × 20`, where `seconds = beat × 60 / BPM`
  (20 units per second, "TIME_SCALE"). Confirmed: tick 576 at 120 BPM = 9 beats = 4.5 s → z = 90.0.
* Numbers come from 32-bit floats in the editor (e.g. `0.00120002031`, `-0.680500031`). Compare
  positions with a tolerance, never with `==`.
* **Offset**: `Offset` in ms (written as a float, e.g. `0.0`). Changing the offset in the helper shifts every object by the same
  amount, so ticks/z are measured from audio start and the offset only moves the grid. **(verify)**

## Coordinates

`Position` is `[x, y, z]` in **metres** (x right, y up). The editor grid is 1 cell = **0.1365 m**,
with a small snap error: `x = grid_x × 0.1365 + 0.002`, `y = grid_y × 0.1365 + 0.0012`.
Confirmed by the sample: every note x/y lands
exactly on a grid cell (e.g. x = −0.6805 = −5 cells, y = 0.0012 = row 0). The editor grid is about **17 × 13 cells** (x −8…+8, y −6…+6) around the center (0, 0). The center is
roughly chest height. Values are rounded to 11 decimals.

## Beatmap JSON (`beatmap.meta.bin`)

Difficulty names are exactly: `Easy`, `Normal`, `Hard`, `Expert`, `Master`, `Custom`. Every
per-difficulty object below has all six keys, with empty values for unused difficulties.

**Key set depends on editor version.** Editor 1.9.7 writes no `Squares`, `Triangles`, `BeatModified` or
`Explicit` keys; the helper (and newer/beta editors) write them. Our codec writes back exactly the keys it
read. New maps get the full set below, which the helper's output shows the editor accepts. **(verify in
Milestone 3 by opening a generated map)**

```jsonc
{
  "Name": "Song title",
  "Author": "Artist",                    // yes: artist goes in "Author"
  "Artwork": "cover.png",                // or "No cover"
  "ArtworkBytes": "<base64>",            // may be null
  "AudioName": "Song_1a2b3c4d.ogg",      // zip entry of the audio
  "AudioData": null,
  "AudioFrecuency": 44100,               // sic
  "AudioChannels": 2,
  "BPM": 128.0,
  "Offset": 0,                           // ms
  "Track":   { "<Diff>": { "<tick>": [ Note, ... ] } },  // notes and rails
  "Effects": { "<Diff>": [ tick, ... ] },
  "Lights":  { "<Diff>": [ tick, ... ] },
  "Jumps":   { "<Diff>": [] },           // unused
  "Slides":  { "<Diff>": [ Wall ... ] }, // side/angled/center walls
  "Crouchs": { "<Diff>": [ Wall ... ] }, // sic
  "Squares": { "<Diff>": [ Wall ... ] },
  "Triangles": { "<Diff>": [ Wall ... ] },
  "Bookmarks": { "BookmarksList": [ { "time": tick, "name": "Drop" } ] },
  "DrumSamples": null,
  "FilePath": "…",
  "IsAdminOnly": false,
  "EditorVersion": "…",
  "Beatmapper": "Mapper name",
  "CustomDifficultyName": "Custom",
  "CustomDifficultySpeed": 1.0,
  "UsingBeatMeasure": true,
  "UpdatedWithMovementPositions": true,
  "ProductionMode": false,
  "Tags": [],
  "BeatConverted": false,
  "BeatModified": false,
  "ModifiedTime": 1732099200,            // unix seconds
  "Explicit": false
}
```

### Note

```jsonc
{
  "Id": "Note_128RightHanded0",   // "Note_<tick><TypeString><n>"
  "ComboId": -1,
  "Position": [x, y, z],
  "Segments": null,               // or [[x,y,z], ...] for a rail (see below)
  "Type": 0,
  "Direction": 0
}
```

| Type | String | Meaning | Color |
|---|---|---|---|
| 0 | `RightHanded` | right hand | pink/red |
| 1 | `LeftHanded` | left hand | blue/cyan |
| 2 | `OneHandSpecial` | either hand ("single") | green |
| 3 | `BothHandsSpecial` | both hands together | yellow |

`Id`: in editor output `<n>` is a running counter across the whole difficulty (1, 2, 3, ... in time
order), not an index within the tick. The helper uses a per-tick index and the editor still loads it, so the
Id looks cosmetic. We use the editor's style.

`ComboId`: `-1` for normal notes. In the sample, special notes (types 2 and 3) sometimes get `0`, `1`, ...
and two consecutive both-hands specials share `1`, so it seems to group consecutive specials into one
combo. Generated maps write `-1`. **(verify)**

There is at most **one note of each type per tick**, so a tick's list holds up to 4 notes.

### Rails

A rail is a note whose `Segments` array is non-null. `Position` is the rail head and `Segments` lists the
following nodes in time order, each as absolute `[x, y, z]`. The game draws a smooth spline through the
nodes. Editor rails in the sample have just one segment (the end point), e.g. head z = 580 → end z = 610. The rail's end time is the z of the last segment. The rail is keyed only by its head tick.

### Walls

```jsonc
{ "time": tick, "slideType": 0..4, "position": [x, y, z], "zRotation": deg, "initialized": true }
```

In editor output wall `position` is `[0, 0, z]`: x/y = 0 means the default place for that wall
type, and z follows the same seconds × 20 rule as notes (checked by tests).

`Slides` uses `slideType`: 0 = right wall, 1 = left wall, 2 = angled right, 3 = center, 4 = angled left.
`Crouchs`/`Squares`/`Triangles` use the same object without `slideType`. Crouch walls also have no
`zRotation`. The helper notes that the game shows about 80 walls at once (PC), so walls should be sparse.

## track.data.json

```json
{
    "name": "Song title",
    "artist": "Artist",
    "duration": "03:45",
    "coverImage": "cover.png",
    "audioFile": "Song_1a2b3c4d.ogg",
    "supportedDifficulties": ["", "Normal", "Hard", "Expert", "", ""],
    "bpm": 128.0,
    "mapper": "neon-cartographer"
}
```

Confirmed in the sample. `bpm` is written as `120.0` and `mapper` may be empty. Note: in the sample
`audioFile` is `"All Saints - Pure Shores.ogg"` (the original import name), while the zip entry and
`AudioName` are `PureShores.ogg`. So `AudioName` in the beatmap decides which audio entry is used, and
`audioFile` is informational. We write the same name in both.

`supportedDifficulties` has six slots in the order above. A slot holds the name if that difficulty has
content, otherwise an empty string.

## Round-trip policy (`internal/synthfile`)

Known fields are typed. Unknown fields at any level are kept as raw JSON and written back, so an editor
upgrade can't silently drop data. Other zip entries keep their bytes and order. The round-trip test
compares the JSON semantically (decoded to generic values), not byte for byte.
