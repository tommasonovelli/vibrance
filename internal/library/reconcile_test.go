package library

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The versions of ffmpeg of these tests: the current one, and the one
// before it.
const (
	fpNow    = "8.1.3-musiclib1"
	fpBefore = "8.0.1-musiclib1"
)

func msec(n int64) *int64 { return &n }

// row is a row of the index as a scan leaves it: available, a FLAC track of
// two seconds, with a fingerprint of the current ffmpeg.
func row(id string, disc, no int, path, sha, fingerprint string, occurrence int) OldTrack {
	return OldTrack{
		ID: id, RelPath: path, FileSHA256: sha, Disc: disc, No: no, DurationMS: msec(2000), Codec: "flac",
		Fingerprint: fingerprint, FPVersion: fpNow, Occurrence: occurrence, Available: true,
	}
}

// unavailable is the row after its file went away.
func unavailable(o OldTrack) OldTrack {
	o.Available = false
	return o
}

// before is the row with the fingerprint that the ffmpeg before the
// current one computed.
func before(o OldTrack, fingerprint string) OldTrack {
	o.Fingerprint, o.FPVersion = fingerprint, fpBefore
	return o
}

// file is a FLAC track file of two seconds of the new receipt, with the
// fingerprint the current ffmpeg computes of it.
func file(disc, no int, path, sha, fingerprint string) NewFile {
	return NewFile{
		RelPath: path, FileSHA256: sha, Disc: disc, No: no, DurationMS: msec(2000), Codec: "flac",
		Fingerprint: fingerprint,
	}
}

func phaseName(p Phase) string {
	switch p {
	case PhaseContent:
		return "F1"
	case PhaseFingerprint:
		return "F2"
	case PhaseWeak:
		return "F3"
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// outcome is a plan as a person reads it. files says, for the path of each
// file, the row that continues as it, the phase that paired them and the
// occurrence of the row, as in "a1 F2 #1", or "new #2" for a new row with
// that occurrence. gone are the ids of the rows that become unavailable.
type outcome struct {
	files map[string]string
	gone  []string
}

func describe(olds []OldTrack, news []NewFile, plan Plan) outcome {
	out := outcome{files: map[string]string{}}
	for _, m := range plan.Matches {
		out.files[news[m.New].RelPath] = fmt.Sprintf("%s %s #%d", olds[m.Old].ID, phaseName(m.Phase), m.Occurrence)
	}
	for _, ins := range plan.Inserts {
		out.files[news[ins.New].RelPath] = fmt.Sprintf("new #%d", ins.Occurrence)
	}
	for _, i := range plan.Gone {
		out.gone = append(out.gone, olds[i].ID)
	}
	slices.Sort(out.gone)
	return out
}

// withFingerprints returns the files with a fingerprint only for those
// whose index is in need: what the indexer has when it calls Reconcile.
func withFingerprints(news []NewFile, need []int) []NewFile {
	out := slices.Clone(news)
	for j := range out {
		if !slices.Contains(need, j) {
			out[j].Fingerprint = ""
		}
	}
	return out
}

// Album A of the fixture library: three tracks with three different
// audios.
var (
	a1 = row("a1", 1, 1, "01 - First Light.flac", "sha-a1", "fp-first", 1)
	a2 = row("a2", 1, 2, "02 - Second Wave.flac", "sha-a2", "fp-second", 1)
	a3 = row("a3", 1, 3, "03 - Third_.flac", "sha-a3", "fp-third", 1)
)

// Album E: two discs.
var (
	e1 = row("e1", 1, 1, "Disc 1/01 - Disc One Track One.flac", "sha-e1", "fp-e1", 1)
	e2 = row("e2", 1, 2, "Disc 1/02 - Disc One Track Two.flac", "sha-e2", "fp-e2", 1)
	e3 = row("e3", 2, 1, "Disc 2/01 - Disc Two Track One.flac", "sha-e3", "fp-e3", 1)
)

// Album F: two tracks with the same audio, so one fingerprint and two
// occurrences.
var (
	f1 = row("f1", 1, 1, "01 - Same Audio.flac", "sha-f1", "fp-same", 1)
	f2 = row("f2", 1, 2, "02 - Same Audio Again.flac", "sha-f2", "fp-same", 2)
)

// The scenarios of the contract with MusicLib (DESIGN.md §12.3), as the
// planner sees them: the rows of the album before, the track files of the
// new receipt after. A change of a tag rewrites the file, so its SHA-256
// changes and its fingerprint does not (§4.3). The files carry the
// fingerprint the current ffmpeg would compute; the test also runs the plan
// with only the fingerprints NeedFingerprint asks for, as the indexer does.
func TestReconcileScenarios(t *testing.T) {
	tests := []struct {
		name string
		olds []OldTrack
		news []NewFile
		// need are the paths of the files NeedFingerprint must name.
		need  []string
		files map[string]string
		gone  []string
	}{
		{
			name: "A1: the title of a track changes",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - First Light (edited).flac", "sha-a1-edited", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
			},
			need: []string{"01 - First Light (edited).flac"},
			files: map[string]string{
				"01 - First Light (edited).flac": "a1 F2 #1",
				"02 - Second Wave.flac":          "a2 F1 #1",
				"03 - Third_.flac":               "a3 F1 #1",
			},
		},
		{
			name: "A2: two tracks swap their numbers: the ids follow the audio",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - Second Wave.flac", "sha-a2-moved", "fp-second"),
				file(1, 2, "02 - First Light.flac", "sha-a1-moved", "fp-first"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
			},
			need: []string{"01 - Second Wave.flac", "02 - First Light.flac"},
			files: map[string]string{
				"01 - Second Wave.flac": "a2 F2 #1",
				"02 - First Light.flac": "a1 F2 #1",
				"03 - Third_.flac":      "a3 F1 #1",
			},
		},
		{
			name: "A3: the cover changes: every file is rewritten, with the same audio",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-cover", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2-cover", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3-cover", "fp-third"),
			},
			need: []string{"01 - First Light.flac", "02 - Second Wave.flac", "03 - Third_.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F2 #1",
				"02 - Second Wave.flac": "a2 F2 #1",
				"03 - Third_.flac":      "a3 F2 #1",
			},
		},
		{
			// A4, A5 and A6 are one case for the planner: the album or the
			// artist tag of every track changes, and the paths, which are
			// relative to the album folder, do not.
			name: "A4, A5, A6: the album or its artist is renamed, on two discs",
			olds: []OldTrack{e1, e2, e3},
			news: []NewFile{
				file(1, 1, "Disc 1/01 - Disc One Track One.flac", "sha-e1-renamed", "fp-e1"),
				file(1, 2, "Disc 1/02 - Disc One Track Two.flac", "sha-e2-renamed", "fp-e2"),
				file(2, 1, "Disc 2/01 - Disc Two Track One.flac", "sha-e3-renamed", "fp-e3"),
			},
			need: []string{
				"Disc 1/01 - Disc One Track One.flac", "Disc 1/02 - Disc One Track Two.flac", "Disc 2/01 - Disc Two Track One.flac",
			},
			files: map[string]string{
				"Disc 1/01 - Disc One Track One.flac": "e1 F2 #1",
				"Disc 1/02 - Disc One Track Two.flac": "e2 F2 #1",
				"Disc 2/01 - Disc Two Track One.flac": "e3 F2 #1",
			},
		},
		{
			// MusicLib does not renumber the tracks that stay, and rewrites
			// them: the total of the tracks is in their tags.
			name: "A7: a track is deleted",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-of-two", "fp-first"),
				file(1, 3, "03 - Third_.flac", "sha-a3-of-two", "fp-third"),
			},
			need: []string{"01 - First Light.flac", "03 - Third_.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F2 #1",
				"03 - Third_.flac":      "a3 F2 #1",
			},
			gone: []string{"a2"},
		},
		{
			name: "A7, then the track comes back with other tags: its row returns",
			olds: []OldTrack{a1, unavailable(a2), a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave (again).flac", "sha-a2-again", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
			},
			need: []string{"02 - Second Wave (again).flac"},
			files: map[string]string{
				"01 - First Light.flac":         "a1 F1 #1",
				"02 - Second Wave (again).flac": "a2 F2 #1",
				"03 - Third_.flac":              "a3 F1 #1",
			},
		},
		{
			// The scanner marks an album that is not seen without a plan
			// (§6.4); a plan for no file says the same.
			name: "A8: no file of the album is left",
			olds: []OldTrack{a1, a2, a3},
			news: nil,
			gone: []string{"a1", "a2", "a3"},
		},
		{
			name: "A9: the album comes back from the trash, with the same files",
			olds: []OldTrack{unavailable(a1), unavailable(a2), unavailable(a3)},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
			},
			files: map[string]string{
				"01 - First Light.flac": "a1 F1 #1",
				"02 - Second Wave.flac": "a2 F1 #1",
				"03 - Third_.flac":      "a3 F1 #1",
			},
		},
		{
			// A forced render, a rebuild, a new render_version that leaves
			// the bytes as they are: nothing is examined.
			name: "A10: the album is rendered again and no file changes",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(1, 1, "01 - Same Audio.flac", "sha-f1", "fp-same"),
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2", "fp-same"),
			},
			files: map[string]string{
				"01 - Same Audio.flac":       "f1 F1 #1",
				"02 - Same Audio Again.flac": "f2 F1 #2",
			},
		},
		{
			name: "A11, A16: every row was unavailable and the files come back rewritten",
			olds: []OldTrack{unavailable(a1), unavailable(a2), unavailable(f1), unavailable(f2)},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-back", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2-back", "fp-second"),
				file(1, 3, "03 - Same Audio.flac", "sha-f1-back", "fp-same"),
				file(1, 4, "04 - Same Audio Again.flac", "sha-f2-back", "fp-same"),
			},
			need: []string{"01 - First Light.flac", "02 - Second Wave.flac", "03 - Same Audio.flac", "04 - Same Audio Again.flac"},
			files: map[string]string{
				"01 - First Light.flac":      "a1 F2 #1",
				"02 - Second Wave.flac":      "a2 F2 #1",
				"03 - Same Audio.flac":       "f1 F2 #1",
				"04 - Same Audio Again.flac": "f2 F2 #2",
			},
		},
		{
			// The occurrences follow the disc and the number, not the path:
			// disc 2 is before disc 10, and "Disc 10" before "Disc 2".
			name: "A12: an album is indexed for the first time",
			olds: nil,
			news: []NewFile{
				file(10, 1, "Disc 10/01 - Same Audio Again.flac", "sha-f2", "fp-same"),
				file(2, 1, "Disc 2/01 - Same Audio.flac", "sha-f1", "fp-same"),
				file(2, 2, "Disc 2/02 - Other.flac", "sha-o", "fp-other"),
			},
			need: []string{"Disc 10/01 - Same Audio Again.flac", "Disc 2/01 - Same Audio.flac", "Disc 2/02 - Other.flac"},
			files: map[string]string{
				"Disc 2/01 - Same Audio.flac":        "new #1",
				"Disc 10/01 - Same Audio Again.flac": "new #2",
				"Disc 2/02 - Other.flac":             "new #1",
			},
		},
		{
			name: "a track is added at the end",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
				file(1, 4, "04 - Fourth.flac", "sha-a4", "fp-fourth"),
			},
			need: []string{"04 - Fourth.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F1 #1",
				"02 - Second Wave.flac": "a2 F1 #1",
				"03 - Third_.flac":      "a3 F1 #1",
				"04 - Fourth.flac":      "new #1",
			},
		},
		{
			name: "a track is inserted in the middle and the ones after it move",
			olds: []OldTrack{a1, a2, a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-of-four", "fp-first"),
				file(1, 2, "02 - Inserted.flac", "sha-inserted", "fp-inserted"),
				file(1, 3, "03 - Second Wave.flac", "sha-a2-of-four", "fp-second"),
				file(1, 4, "04 - Third_.flac", "sha-a3-of-four", "fp-third"),
			},
			need: []string{"01 - First Light.flac", "02 - Inserted.flac", "03 - Second Wave.flac", "04 - Third_.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F2 #1",
				"02 - Inserted.flac":    "new #1",
				"03 - Second Wave.flac": "a2 F2 #1",
				"04 - Third_.flac":      "a3 F2 #1",
			},
		},
		{
			name: "a track with the audio of another is added: the next occurrence",
			olds: []OldTrack{a1, a2},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2", "fp-second"),
				file(1, 3, "03 - First Light (reprise).flac", "sha-reprise", "fp-first"),
			},
			need: []string{"03 - First Light (reprise).flac"},
			files: map[string]string{
				"01 - First Light.flac":           "a1 F1 #1",
				"02 - Second Wave.flac":           "a2 F1 #1",
				"03 - First Light (reprise).flac": "new #2",
			},
		},
		{
			name: "the audio of a track is replaced: another track in its place",
			olds: []OldTrack{a1, a2},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2-remaster", "fp-second-remaster"),
			},
			need: []string{"02 - Second Wave.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F1 #1",
				"02 - Second Wave.flac": "new #1",
			},
			gone: []string{"a2"},
		},

		// A14 and the other cases of two tracks with one audio.
		{
			name: "A14: of two tracks with the same audio the first is deleted: the second keeps its id",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2-alone", "fp-same"),
			},
			need:  []string{"02 - Same Audio Again.flac"},
			files: map[string]string{"02 - Same Audio Again.flac": "f2 F2 #2"},
			gone:  []string{"f1"},
		},
		{
			name: "A14, with the file of the second unchanged",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2", "fp-same"),
			},
			files: map[string]string{"02 - Same Audio Again.flac": "f2 F1 #2"},
			gone:  []string{"f1"},
		},
		{
			name: "A14, then another copy of the audio is added: it is the first copy, back",
			olds: []OldTrack{unavailable(f1), f2, a3},
			news: []NewFile{
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2", "fp-same"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
				file(1, 4, "04 - Same Audio Once More.flac", "sha-f4", "fp-same"),
			},
			need: []string{"04 - Same Audio Once More.flac"},
			files: map[string]string{
				"02 - Same Audio Again.flac":     "f2 F1 #2",
				"03 - Third_.flac":               "a3 F1 #1",
				"04 - Same Audio Once More.flac": "f1 F2 #1",
			},
		},
		{
			name: "two tracks with the same audio are both rewritten: each row keeps its place",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(1, 1, "01 - Same Audio Again.flac", "sha-f1-retitled", "fp-same"),
				file(1, 2, "02 - Same Audio.flac", "sha-f2-retitled", "fp-same"),
			},
			need: []string{"01 - Same Audio Again.flac", "02 - Same Audio.flac"},
			files: map[string]string{
				"01 - Same Audio Again.flac": "f1 F2 #1",
				"02 - Same Audio.flac":       "f2 F2 #2",
			},
		},
		{
			// The files in the order of (disc, no), which is not that of
			// their paths, and the rows in that of their occurrences.
			name: "two tracks with the same audio move to other discs: by disc and number, and by occurrence",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(10, 1, "Disc 10/01 - Same Audio Again.flac", "sha-f2-moved", "fp-same"),
				file(2, 7, "Disc 2/07 - Same Audio.flac", "sha-f1-moved", "fp-same"),
			},
			need: []string{"Disc 10/01 - Same Audio Again.flac", "Disc 2/07 - Same Audio.flac"},
			files: map[string]string{
				"Disc 2/07 - Same Audio.flac":        "f1 F2 #1",
				"Disc 10/01 - Same Audio Again.flac": "f2 F2 #2",
			},
		},
		{
			// The pair in the same place is made first, whatever the
			// occurrences: the file at 1-2 is the row at 1-2.
			name: "of two tracks with the same audio one moves and one stays",
			olds: []OldTrack{f1, f2},
			news: []NewFile{
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2-rewritten", "fp-same"),
				file(1, 9, "09 - Same Audio.flac", "sha-f1-moved", "fp-same"),
			},
			need: []string{"02 - Same Audio Again.flac", "09 - Same Audio.flac"},
			files: map[string]string{
				"02 - Same Audio Again.flac": "f2 F2 #2",
				"09 - Same Audio.flac":       "f1 F2 #1",
			},
		},

		// F3: ffmpeg changed version, and the album changed before the
		// fingerprints of its rows were computed again.
		{
			name: "F3: the version of ffmpeg changes and every file is rewritten",
			olds: []OldTrack{before(a1, "old-first"), before(a2, "old-second"), before(a3, "old-third")},
			news: []NewFile{
				file(1, 1, "01 - First Light (edited).flac", "sha-a1-edited", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2-edited", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3-edited", "fp-third"),
			},
			need: []string{"01 - First Light (edited).flac", "02 - Second Wave.flac", "03 - Third_.flac"},
			files: map[string]string{
				"01 - First Light (edited).flac": "a1 F3 #1",
				"02 - Second Wave.flac":          "a2 F3 #1",
				"03 - Third_.flac":               "a3 F3 #1",
			},
		},
		{
			name: "F3: the version changes and only one file is rewritten: the others are the same bytes",
			olds: []OldTrack{before(a1, "old-first"), before(a2, "old-second"), before(a3, "old-third")},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
				file(1, 2, "02 - Second Wave (edited).flac", "sha-a2-edited", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
			},
			need: []string{"02 - Second Wave (edited).flac"},
			files: map[string]string{
				"01 - First Light.flac":          "a1 F1 #1",
				"02 - Second Wave (edited).flac": "a2 F3 #1",
				"03 - Third_.flac":               "a3 F1 #1",
			},
		},
		{
			// a1 was computed again already; a2 was not, and its file is in
			// its place; in the place of a3, whose fingerprint is current,
			// there is another audio: a3 is not paired.
			name: "F3: only the rows of the version before are paired by their place",
			olds: []OldTrack{a1, before(a2, "old-second"), a3},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-edited", "fp-first"),
				file(1, 2, "02 - Second Wave.flac", "sha-a2-edited", "fp-second"),
				file(1, 3, "03 - Third_.flac", "sha-a3-edited", "fp-not-the-third"),
			},
			need: []string{"01 - First Light.flac", "02 - Second Wave.flac", "03 - Third_.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F2 #1",
				"02 - Second Wave.flac": "a2 F3 #1",
				"03 - Third_.flac":      "new #1",
			},
			gone: []string{"a3"},
		},
		{
			name: "F3: another number, another disc, another codec or another duration is another track",
			olds: []OldTrack{
				before(a1, "old-first"), before(a2, "old-second"), before(a3, "old-third"), before(e3, "old-e3"),
			},
			news: []NewFile{
				file(1, 4, "04 - First Light.flac", "sha-a1-moved", "fp-first"),
				file(2, 2, "Disc 2/02 - Second Wave.flac", "sha-a2-moved", "fp-second"),
				{RelPath: "03 - Third_.m4a", FileSHA256: "sha-a3-alac", Disc: 1, No: 3, DurationMS: msec(2000), Codec: "alac", Fingerprint: "fp-third"},
				{RelPath: "Disc 2/01 - Longer.flac", FileSHA256: "sha-e3-longer", Disc: 2, No: 1, DurationMS: msec(2001), Codec: "flac", Fingerprint: "fp-e3"},
			},
			need: []string{"04 - First Light.flac", "Disc 2/02 - Second Wave.flac", "03 - Third_.m4a", "Disc 2/01 - Longer.flac"},
			files: map[string]string{
				"04 - First Light.flac":        "new #1",
				"Disc 2/02 - Second Wave.flac": "new #1",
				"03 - Third_.m4a":              "new #1",
				"Disc 2/01 - Longer.flac":      "new #1",
			},
			gone: []string{"a1", "a2", "a3", "e3"},
		},
		{
			name: "F3: a duration that is not known is not the same duration",
			olds: []OldTrack{
				{ID: "u1", RelPath: "01.mp3", FileSHA256: "sha-u1", Disc: 1, No: 1, Codec: "mp3",
					Fingerprint: "old-u1", FPVersion: fpBefore, Occurrence: 1, Available: true},
				{ID: "u2", RelPath: "02.mp3", FileSHA256: "sha-u2", Disc: 1, No: 2, Codec: "mp3", DurationMS: msec(2000),
					Fingerprint: "old-u2", FPVersion: fpBefore, Occurrence: 1, Available: true},
			},
			news: []NewFile{
				{RelPath: "01.mp3", FileSHA256: "sha-u1-edited", Disc: 1, No: 1, Codec: "mp3", Fingerprint: "fp-u1"},
				{RelPath: "02.mp3", FileSHA256: "sha-u2-edited", Disc: 1, No: 2, Codec: "mp3", Fingerprint: "fp-u2"},
			},
			need:  []string{"01.mp3", "02.mp3"},
			files: map[string]string{"01.mp3": "new #1", "02.mp3": "new #1"},
			gone:  []string{"u1", "u2"},
		},
		{
			// The fingerprint of a row of another version is not compared,
			// even if it is the same text: F2 does not pair it. Here F3
			// does, by the place.
			name: "F3: the new ffmpeg computes the fingerprint the old one did",
			olds: []OldTrack{before(a1, "fp-first"), before(a2, "fp-second")},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1-edited", "fp-first"),
				file(1, 5, "05 - Second Wave.flac", "sha-a2-moved", "fp-second"),
			},
			need: []string{"01 - First Light.flac", "05 - Second Wave.flac"},
			files: map[string]string{
				"01 - First Light.flac": "a1 F3 #1",
				// Not paired, and a2 holds the first occurrence of that
				// fingerprint.
				"05 - Second Wave.flac": "new #2",
			},
			gone: []string{"a2"},
		},
		{
			// The two rows had one fingerprint of the version before, and
			// take the new one: their occurrences must not be the one of
			// the third copy, whose row has it already.
			name: "F3: rows that take a fingerprint the album has take other occurrences",
			olds: []OldTrack{
				before(f1, "old-same"), before(f2, "old-same"),
				row("f3", 1, 3, "03 - Same Audio Thrice.flac", "sha-f3", "fp-same", 1),
			},
			news: []NewFile{
				file(1, 1, "01 - Same Audio.flac", "sha-f1-edited", "fp-same"),
				file(1, 2, "02 - Same Audio Again.flac", "sha-f2-edited", "fp-same"),
				file(1, 3, "03 - Same Audio Thrice.flac", "sha-f3", "fp-same"),
			},
			need: []string{"01 - Same Audio.flac", "02 - Same Audio Again.flac"},
			files: map[string]string{
				"01 - Same Audio.flac":        "f1 F3 #2",
				"02 - Same Audio Again.flac":  "f2 F3 #3",
				"03 - Same Audio Thrice.flac": "f3 F1 #1",
			},
		},

		// The order of the phases, and the order inside each.
		{
			// The file "a.flac" is, byte for byte, the file of t1, and F1
			// pairs them. F2 alone would pair it with t2, which is in its
			// place and has its audio.
			name: "F1 comes before F2",
			olds: []OldTrack{
				row("t1", 1, 1, "x.flac", "sha-1", "fp-same", 1),
				row("t2", 1, 2, "y.flac", "sha-2", "fp-same", 2),
			},
			news: []NewFile{
				file(1, 2, "a.flac", "sha-1", "fp-same"),
				file(1, 1, "b.flac", "sha-3", "fp-same"),
			},
			need:  []string{"b.flac"},
			files: map[string]string{"a.flac": "t1 F1 #1", "b.flac": "t2 F2 #2"},
		},
		{
			// F3 would pair the file with s1, which is in its place; F2
			// pairs it with the row that has its audio.
			name: "F2 comes before F3",
			olds: []OldTrack{
				before(row("s1", 1, 1, "x.flac", "sha-1", "old-x", 1), "old-x"),
				row("s2", 1, 2, "y.flac", "sha-2", "fp-y", 1),
			},
			news: []NewFile{
				file(1, 1, "a.flac", "sha-3", "fp-y"),
			},
			need:  []string{"a.flac"},
			files: map[string]string{"a.flac": "s2 F2 #1"},
			gone:  []string{"s1"},
		},
		{
			name: "F1: several rows and files with one SHA-256 are paired in the order (disc, no, rel_path)",
			olds: []OldTrack{
				row("d3", 2, 1, "c.flac", "sha-same", "fp-d", 3),
				row("d2", 1, 2, "b.flac", "sha-same", "fp-d", 2),
				row("d1", 1, 1, "a.flac", "sha-same", "fp-d", 1),
				row("d0", 1, 1, "0.flac", "sha-other", "fp-0", 1),
			},
			news: []NewFile{
				file(2, 1, "y.flac", "sha-same", "fp-d"),
				file(1, 1, "z.flac", "sha-same", "fp-d"),
			},
			files: map[string]string{"z.flac": "d1 F1 #1", "y.flac": "d2 F1 #2"},
			gone:  []string{"d0", "d3"},
		},
		{
			name: "F1: of the rows with one SHA-256 and one place, the available one",
			olds: []OldTrack{
				unavailable(row("p1", 1, 1, "a.flac", "sha-same", "fp-p", 1)),
				row("p2", 1, 1, "a.flac", "sha-same", "fp-p", 2),
			},
			news:  []NewFile{file(1, 1, "a.flac", "sha-same", "fp-p")},
			files: map[string]string{"a.flac": "p2 F1 #2"},
		},
		{
			name: "F4: the lowest occurrence no row has, and no other new row of the plan",
			olds: []OldTrack{
				// Rows of the version before, in other places: neither F2
				// nor F3 pairs them, and their occurrences are taken.
				unavailable(before(row("x1", 1, 1, "01.flac", "sha-x1", "fp-x", 1), "fp-x")),
				before(row("x3", 1, 3, "03.flac", "sha-x3", "fp-x", 3), "fp-x"),
				row("y1", 2, 1, "y.flac", "sha-y1", "fp-y", 1),
			},
			news: []NewFile{
				file(1, 9, "09.flac", "sha-n9", "fp-x"),
				file(1, 8, "08.flac", "sha-n8", "fp-x"),
				file(1, 7, "07.flac", "sha-n7", "fp-x"),
				file(2, 1, "y.flac", "sha-y1", "fp-y"),
				file(2, 2, "z.flac", "sha-z", "fp-z"),
			},
			need: []string{"09.flac", "08.flac", "07.flac", "z.flac"},
			files: map[string]string{
				"07.flac": "new #2",
				"08.flac": "new #4",
				"09.flac": "new #5",
				"y.flac":  "y1 F1 #1",
				"z.flac":  "new #1",
			},
			gone: []string{"x3"},
		},
		{
			name: "F5: a row that was not available and has no file stays as it is",
			olds: []OldTrack{a1, unavailable(a2), unavailable(a3)},
			news: []NewFile{
				file(1, 1, "01 - First Light.flac", "sha-a1", "fp-first"),
			},
			files: map[string]string{"01 - First Light.flac": "a1 F1 #1"},
		},
		{
			name: "nothing before, nothing after",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			need := NeedFingerprint(tc.olds, tc.news)
			var needPaths []string
			for _, j := range need {
				needPaths = append(needPaths, tc.news[j].RelPath)
			}
			if !slices.Equal(needPaths, tc.need) {
				t.Errorf("NeedFingerprint names %q, want %q", needPaths, tc.need)
			}
			if !slices.IsSorted(need) {
				t.Errorf("NeedFingerprint = %v is not in ascending order", need)
			}

			want := outcome{files: tc.files, gone: tc.gone}
			if want.files == nil {
				want.files = map[string]string{}
			}
			// With every fingerprint, and with only those the indexer
			// computes.
			for _, news := range [][]NewFile{tc.news, withFingerprints(tc.news, need)} {
				plan, err := Reconcile(tc.olds, news, fpNow)
				if err != nil {
					t.Fatalf("Reconcile: %v", err)
				}
				if got := describe(tc.olds, news, plan); !reflect.DeepEqual(got, want) {
					t.Errorf("the plan is\n%s\nwant\n%s", got, want)
				}
				if err := verifyPlan(tc.olds, news, plan, fpNow); err != nil {
					t.Errorf("the plan breaks a rule: %v", err)
				}
			}
		})
	}
}

func (o outcome) String() string {
	var b strings.Builder
	paths := make([]string, 0, len(o.files))
	for p := range o.files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "  %-40q %s\n", p, o.files[p])
	}
	fmt.Fprintf(&b, "  gone: %q", o.gone)
	return b.String()
}

// A file that F1 does not pair must come with its fingerprint: without it
// the plan would make a new track of a track that only changed its tags.
func TestReconcileRefusesAFileWithoutFingerprint(t *testing.T) {
	olds := []OldTrack{a1, a2}
	news := []NewFile{
		file(1, 1, "01 - First Light.flac", "sha-a1", ""),
		file(1, 2, "02 - Second Wave (edited).flac", "sha-a2-edited", ""),
	}
	if need := NeedFingerprint(olds, news); !slices.Equal(need, []int{1}) {
		t.Fatalf("NeedFingerprint = %v, want [1]", need)
	}
	plan, err := Reconcile(olds, news, fpNow)
	if err == nil {
		t.Fatalf("Reconcile returned the plan %+v for a file without a fingerprint", plan)
	}
	if !strings.Contains(err.Error(), "02 - Second Wave (edited).flac") {
		t.Errorf("the error does not name the file: %v", err)
	}
	if !reflect.DeepEqual(plan, Plan{}) {
		t.Errorf("Reconcile returned a plan with its error: %+v", plan)
	}

	// With the fingerprint it asked for, the same call is a plan.
	news[1].Fingerprint = "fp-second"
	plan, err = Reconcile(olds, news, fpNow)
	if err != nil {
		t.Fatal(err)
	}
	want := outcome{files: map[string]string{
		"01 - First Light.flac":          "a1 F1 #1",
		"02 - Second Wave (edited).flac": "a2 F2 #1",
	}}
	if got := describe(olds, news, plan); !reflect.DeepEqual(got, want) {
		t.Errorf("the plan is\n%s\nwant\n%s", got, want)
	}

	// An album that is new needs every fingerprint.
	if _, err := Reconcile(nil, []NewFile{file(1, 1, "a.flac", "sha", "")}, fpNow); err == nil {
		t.Error("Reconcile accepted a new file without a fingerprint")
	}
}

// The planner reads its arguments and changes nothing of them.
func TestReconcileLeavesItsArguments(t *testing.T) {
	olds := []OldTrack{a3, unavailable(a2), before(a1, "old-first"), f2, f1}
	news := []NewFile{
		file(1, 3, "03 - Third_.flac", "sha-a3", "fp-third"),
		file(1, 2, "02 - Second Wave.flac", "sha-a2-edited", "fp-second"),
		file(1, 1, "01 - First Light.flac", "sha-a1-edited", "fp-first"),
		file(1, 5, "05 - Same Audio.flac", "sha-f-edited", "fp-same"),
	}
	oldsBefore, newsBefore := slices.Clone(olds), slices.Clone(news)
	NeedFingerprint(olds, news)
	if _, err := Reconcile(olds, news, fpNow); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(olds, oldsBefore) || !reflect.DeepEqual(news, newsBefore) {
		t.Fatal("the planner changed its arguments")
	}
}

// The life of an album through the scenarios, one plan after the other on
// the rows the plan before left: the ids of the three tracks never change,
// and no row is ever removed.
func TestReconcileThroughTheLifeOfAnAlbum(t *testing.T) {
	ids := newIDs()
	var rows []OldTrack
	scan := func(news ...NewFile) {
		t.Helper()
		need := NeedFingerprint(rows, news)
		given := withFingerprints(news, need)
		plan, err := Reconcile(rows, given, fpNow)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyPlan(rows, given, plan, fpNow); err != nil {
			t.Fatal(err)
		}
		rows = applyPlan(rows, given, plan, fpNow, ids.next)
	}
	// where says, for each file on disk, the id of its row; and the rows
	// that are not available.
	where := func() string {
		t.Helper()
		var parts []string
		for _, r := range rows {
			if r.Available {
				parts = append(parts, fmt.Sprintf("%s=%d-%d %s", r.ID, r.Disc, r.No, r.RelPath))
			} else {
				parts = append(parts, r.ID+"=gone")
			}
		}
		slices.Sort(parts)
		return strings.Join(parts, "; ")
	}
	expect := func(step, want string) {
		t.Helper()
		if got := where(); got != want {
			t.Fatalf("%s:\n got %s\nwant %s", step, got, want)
		}
	}

	scan(file(1, 1, "01 - One.flac", "s1", "fp-1"), file(1, 2, "02 - Two.flac", "s2", "fp-2"), file(1, 3, "03 - Three.flac", "s3", "fp-3"))
	expect("first scan", "id-1=1-1 01 - One.flac; id-2=1-2 02 - Two.flac; id-3=1-3 03 - Three.flac")

	scan(file(1, 1, "01 - Uno.flac", "s1b", "fp-1"), file(1, 2, "02 - Two.flac", "s2", "fp-2"), file(1, 3, "03 - Three.flac", "s3", "fp-3"))
	expect("A1, a title", "id-1=1-1 01 - Uno.flac; id-2=1-2 02 - Two.flac; id-3=1-3 03 - Three.flac")

	scan(file(1, 1, "01 - Two.flac", "s2b", "fp-2"), file(1, 2, "02 - Uno.flac", "s1c", "fp-1"), file(1, 3, "03 - Three.flac", "s3", "fp-3"))
	expect("A2, a swap", "id-1=1-2 02 - Uno.flac; id-2=1-1 01 - Two.flac; id-3=1-3 03 - Three.flac")

	scan(file(1, 1, "01 - Two.flac", "s2c", "fp-2"), file(1, 3, "03 - Three.flac", "s3c", "fp-3"))
	expect("A7, a deletion", "id-1=gone; id-2=1-1 01 - Two.flac; id-3=1-3 03 - Three.flac")

	scan()
	expect("A8, the trash", "id-1=gone; id-2=gone; id-3=gone")

	scan(file(1, 1, "01 - Two.flac", "s2c", "fp-2"), file(1, 3, "03 - Three.flac", "s3c", "fp-3"))
	expect("A9, the restore", "id-1=gone; id-2=1-1 01 - Two.flac; id-3=1-3 03 - Three.flac")

	scan(file(1, 1, "01 - Two.flac", "s2c", "fp-2"), file(1, 3, "03 - Three.flac", "s3c", "fp-3"))
	expect("A10, a rebuild", "id-1=gone; id-2=1-1 01 - Two.flac; id-3=1-3 03 - Three.flac")

	scan(file(1, 1, "01 - Two.flac", "s2d", "fp-2"), file(1, 2, "02 - Uno.flac", "s1d", "fp-1"), file(1, 3, "03 - Three.flac", "s3d", "fp-3"),
		file(1, 4, "04 - Four.flac", "s4", "fp-4"))
	expect("the deleted track is imported again, with a new one",
		"id-1=1-2 02 - Uno.flac; id-2=1-1 01 - Two.flac; id-3=1-3 03 - Three.flac; id-4=1-4 04 - Four.flac")
}
