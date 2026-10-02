package library

import (
	"cmp"
	"fmt"
	"slices"
)

// The reconciliation of the tracks of one album (DESIGN.md §5.4): which
// row of the index continues as which file of the new receipt. It is what
// keeps the id of a track when MusicLib changes its title, its number, its
// path and so the SHA-256 of its file: the audio is what stays.
//
// The planner is pure. It reads its arguments and returns a plan; it
// touches no disk, no database and no clock, and it gives no id to a new
// row: the indexer does, when it applies the plan.

// OldTrack is a row of the album in the index, available or not.
type OldTrack struct {
	// ID is tracks.id. The planner only uses it to order two rows that
	// nothing else tells apart.
	ID         string
	RelPath    string
	FileSHA256 string
	Disc       int
	No         int
	// DurationMS is nil when the duration is unknown.
	DurationMS *int64
	Codec      string
	// Fingerprint is the fingerprint of the audio, as the ffmpeg of
	// FPVersion computed it. Two equal fingerprints are the same audio,
	// whatever ffmpeg computed them.
	Fingerprint string
	FPVersion   string
	// Occurrence tells apart the rows of the album with one fingerprint.
	Occurrence int
	Available  bool
}

// NewFile is a track file of the new receipt of the album.
type NewFile struct {
	RelPath    string
	FileSHA256 string
	Disc       int
	No         int
	// DurationMS is nil when the duration is unknown.
	DurationMS *int64
	Codec      string
	// Fingerprint is the fingerprint of the audio as the current ffmpeg
	// computes it, or "" when it was not computed: only the files that
	// NeedFingerprint names need one.
	Fingerprint string
}

// Phase is the rule that paired a row with a file.
type Phase int

const (
	// PhaseContent is F1: the file has the SHA-256 of the row, so it is
	// the same file, byte for byte.
	PhaseContent Phase = iota + 1
	// PhaseFingerprint is F2: the file has the fingerprint of the row, so
	// it has the same audio.
	PhaseFingerprint
	// PhaseWeak is F3: the fingerprint of the row was computed by another
	// ffmpeg and no file has it, and the file has the disc, number, codec
	// and duration of the row.
	PhaseWeak
)

// Match is a row that continues as a file: the row keeps its id, takes
// every changeable field from the file and is available.
type Match struct {
	// Old and New are indexes in the two arguments of Reconcile.
	Old int
	New int
	// Phase is the rule that paired them. After PhaseContent the file was
	// not examined, and the row keeps its fingerprint and fp_version;
	// after the other two the row takes the fingerprint of the file and
	// the current version.
	Phase Phase
	// Occurrence is the occurrence of the row from now on. It is the one
	// the row had, except after PhaseWeak, which changes the fingerprint
	// of the row.
	Occurrence int
}

// Insert is a file that no row continues as: it becomes a new row.
type Insert struct {
	// New is an index in the files given to Reconcile.
	New int
	// Occurrence is the occurrence of the new row.
	Occurrence int
}

// Plan is what makes the rows of an album those of its new receipt. No row
// is ever removed (I3). A row that is in neither Matches nor Gone was not
// available and stays as it is.
type Plan struct {
	// Matches are the rows that continue, in the order of the rows.
	Matches []Match
	// Inserts are the new rows, in the order (disc, no, rel_path) of their
	// files, which is the order their occurrences were given in.
	Inserts []Insert
	// Gone are the indexes of the rows that were available and have no
	// file any more: they become unavailable (F5).
	Gone []int
}

// NeedFingerprint returns the indexes of the files that F1 does not pair
// with a row, in ascending order: the files whose audio must be examined
// before Reconcile is called. The others are, byte for byte, files the
// index already knows.
//
// It reads only what the index and the receipt say: the SHA-256 and the
// path of the rows and of the files, and which rows are available. So it
// can be called before any file is examined, and Reconcile pairs the same
// files in F1 whatever the tags of the files turn out to be.
func NeedFingerprint(olds []OldTrack, news []NewFile) []int {
	p := newPairing(olds, news)
	p.pairContent()
	var need []int
	for j, i := range p.oldOf {
		if i < 0 {
			need = append(need, j)
		}
	}
	return need
}

// Reconcile pairs the rows of an album with the files of its new receipt,
// by the rules of DESIGN.md §5.4 and of its erratum. fpVersion is the
// version of the ffmpeg that computed the fingerprints of the files, the
// current one. The phases run in their order, and each sees only what the
// ones before left:
//
//   - F1, same content: a file and a row with one SHA-256. If several have
//     it, the available rows come before the others, then the rows are in
//     the order of their paths, and so are the files. The disc and the
//     number have no part in it.
//   - F2, same audio: a file and a row with one fingerprint, whatever the
//     version of the fingerprint of the row. Among those with one
//     fingerprint, the available rows are paired first, and the others
//     only with the files that are left: a track on disk keeps its id
//     before a deleted one takes it back. Each of the two rounds first
//     makes the pairs with the same (disc, no), the rows in the order of
//     their occurrences; then the rest, the files in the order (disc, no,
//     rel_path) and the rows in that of their occurrences.
//   - F3, the weak one, only for the rows whose fingerprint has another
//     version than fpVersion and that F2 left: a file and a row with the
//     same disc, number, codec and duration, which both must know. The
//     files are in the order (disc, no, rel_path); of the rows in one
//     place the available ones come first. A row with the version
//     fpVersion and another fingerprint is not paired: the audio in that
//     place changed.
//   - F4: a file without a row becomes a new row. Its occurrence is the
//     lowest number above zero that no row of the album has for that
//     fingerprint, available or not, and that this plan has not given.
//   - F5: an available row without a file becomes unavailable.
//
// A row that F2 pairs keeps its occurrence and takes the version
// fpVersion. A row that F3 pairs takes another fingerprint (F2 leaves no
// row and file with the same one), so it leaves its occurrence and takes
// one by the rule of F4: (fingerprint, occurrence) names one row of the
// album. The plan never gives an occurrence that a row had before it, so
// its changes can be applied in any order.
//
// The rows must be those of one album: no two with one id, no two with one
// (fingerprint, occurrence). No two files have one path. Where the rules
// leave two rows in one place, the lower id comes first: the plan does not
// depend on the order of the arguments.
//
// Two things the rules cannot know. If two available rows have one audio
// and only one file with that audio is left, moved to another number,
// either row may be the one that stays. And F3 trusts the place and the
// exact duration: a new track with the disc, number, codec and duration in
// milliseconds of a row of another version takes its id.
//
// A file without a fingerprint that F1 does not pair is an error: the
// caller did not examine a file NeedFingerprint named.
func Reconcile(olds []OldTrack, news []NewFile, fpVersion string) (Plan, error) {
	p := newPairing(olds, news)
	p.pairContent()
	for j, i := range p.oldOf {
		if i < 0 && news[j].Fingerprint == "" {
			return Plan{}, fmt.Errorf("the file %q has no fingerprint, and no row of the album has its SHA-256", news[j].RelPath)
		}
	}
	p.pairFingerprint()
	p.pairWeak(fpVersion)
	return p.plan(), nil
}

// pairing is the state of a reconciliation: the rows, the files, and who
// is paired with whom so far.
type pairing struct {
	olds []OldTrack
	news []NewFile
	// newOf is, for each row, the index of its file, or -1; oldOf is the
	// reverse; phase is, for each file with a row, the rule that paired
	// them.
	newOf []int
	oldOf []int
	phase []Phase
}

func newPairing(olds []OldTrack, news []NewFile) *pairing {
	p := &pairing{
		olds:  olds,
		news:  news,
		newOf: make([]int, len(olds)),
		oldOf: make([]int, len(news)),
		phase: make([]Phase, len(news)),
	}
	for i := range p.newOf {
		p.newOf[i] = -1
	}
	for j := range p.oldOf {
		p.oldOf[j] = -1
	}
	return p
}

// availableFirst is the order of the rows that F1 and F3 choose among:
// the available ones, then the others, each in the order of their paths.
func availableFirst(a, b OldTrack) int {
	return cmp.Or(
		cmp.Compare(rank(a.Available), rank(b.Available)),
		cmp.Compare(a.RelPath, b.RelPath),
		cmp.Compare(a.ID, b.ID))
}

// rank is 0 for an available row and 1 for one that is not.
func rank(available bool) int {
	if available {
		return 0
	}
	return 1
}

// byOccurrence is the order of the rows with one fingerprint.
func byOccurrence(a, b OldTrack) int {
	return cmp.Or(cmp.Compare(a.Occurrence, b.Occurrence), cmp.Compare(a.ID, b.ID))
}

// byPath is the order of the files with one SHA-256. It reads no tag.
func byPath(a, b NewFile) int {
	return cmp.Compare(a.RelPath, b.RelPath)
}

// byPlace is the order (disc, no, rel_path) of the files.
func byPlace(a, b NewFile) int {
	return cmp.Or(cmp.Compare(a.Disc, b.Disc), cmp.Compare(a.No, b.No), cmp.Compare(a.RelPath, b.RelPath))
}

// unpairedRows returns the indexes of the rows without a file, in order.
func (p *pairing) unpairedRows(order func(a, b OldTrack) int) []int {
	var rows []int
	for i, j := range p.newOf {
		if j < 0 {
			rows = append(rows, i)
		}
	}
	slices.SortStableFunc(rows, func(a, b int) int { return order(p.olds[a], p.olds[b]) })
	return rows
}

// files returns the indexes of the files in order: all of them, or only
// those without a row.
func (p *pairing) files(order func(a, b NewFile) int, onlyUnpaired bool) []int {
	var files []int
	for j, i := range p.oldOf {
		if i < 0 || !onlyUnpaired {
			files = append(files, j)
		}
	}
	slices.SortStableFunc(files, func(a, b int) int { return order(p.news[a], p.news[b]) })
	return files
}

// pair pairs the rows and the files, among those not paired yet, that have
// the same key: of those with one key, the first row in rowOrder with the
// first file in fileOrder, the second with the second, until the rows or
// the files with that key end. A row or a file whose key function answers
// false takes no part.
func pair[K comparable](p *pairing, phase Phase,
	rowOrder func(a, b OldTrack) int, fileOrder func(a, b NewFile) int,
	rowKey func(OldTrack) (K, bool), fileKey func(NewFile) (K, bool)) {
	rows := map[K][]int{}
	for _, i := range p.unpairedRows(rowOrder) {
		if key, ok := rowKey(p.olds[i]); ok {
			rows[key] = append(rows[key], i)
		}
	}
	for _, j := range p.files(fileOrder, true) {
		key, ok := fileKey(p.news[j])
		if !ok || len(rows[key]) == 0 {
			continue
		}
		i := rows[key][0]
		rows[key] = rows[key][1:]
		p.newOf[i], p.oldOf[j], p.phase[j] = j, i, phase
	}
}

// pairContent is F1. It must read nothing that comes from the tags of a
// file: NeedFingerprint runs it before the files are examined.
func (p *pairing) pairContent() {
	pair(p, PhaseContent, availableFirst, byPath,
		func(o OldTrack) (string, bool) { return o.FileSHA256, true },
		func(n NewFile) (string, bool) { return n.FileSHA256, true })
}

// pairFingerprint is F2: a round for the available rows and one for the
// others, and in each first the pairs with one fingerprint and one place,
// then the rest of those with one fingerprint.
func (p *pairing) pairFingerprint() {
	type audioAt struct {
		fingerprint string
		disc, no    int
	}
	for _, available := range []bool{true, false} {
		pair(p, PhaseFingerprint, byOccurrence, byPlace,
			func(o OldTrack) (audioAt, bool) {
				return audioAt{o.Fingerprint, o.Disc, o.No}, o.Available == available
			},
			func(n NewFile) (audioAt, bool) {
				return audioAt{n.Fingerprint, n.Disc, n.No}, true
			})
		pair(p, PhaseFingerprint, byOccurrence, byPlace,
			func(o OldTrack) (string, bool) { return o.Fingerprint, o.Available == available },
			func(n NewFile) (string, bool) { return n.Fingerprint, true })
	}
}

// pairWeak is F3.
func (p *pairing) pairWeak(fpVersion string) {
	type slot struct {
		disc, no   int
		codec      string
		durationMS int64
	}
	pair(p, PhaseWeak, availableFirst, byPlace,
		func(o OldTrack) (slot, bool) {
			if o.FPVersion == fpVersion || o.DurationMS == nil {
				return slot{}, false
			}
			return slot{o.Disc, o.No, o.Codec, *o.DurationMS}, true
		},
		func(n NewFile) (slot, bool) {
			if n.DurationMS == nil {
				return slot{}, false
			}
			return slot{n.Disc, n.No, n.Codec, *n.DurationMS}, true
		})
}

// plan turns the pairs into the plan: it gives their occurrences to the
// new rows (F4) and to the rows that F3 paired, whose fingerprint changes,
// in the order (disc, no, rel_path) of their files; and it finds the rows
// that are gone (F5).
func (p *pairing) plan() Plan {
	taken := newOccurrences(p.olds)
	changed := map[int]int{} // the new occurrence of a row
	var plan Plan
	for _, j := range p.files(byPlace, false) {
		switch i := p.oldOf[j]; {
		case i < 0:
			plan.Inserts = append(plan.Inserts, Insert{New: j, Occurrence: taken.next(p.news[j].Fingerprint)})
		case p.phase[j] == PhaseWeak:
			changed[i] = taken.next(p.news[j].Fingerprint)
		}
	}
	for i, j := range p.newOf {
		switch {
		case j >= 0:
			occurrence, ok := changed[i]
			if !ok {
				occurrence = p.olds[i].Occurrence
			}
			plan.Matches = append(plan.Matches, Match{Old: i, New: j, Phase: p.phase[j], Occurrence: occurrence})
		case p.olds[i].Available:
			plan.Gone = append(plan.Gone, i)
		}
	}
	return plan
}

// occurrences are the occurrences taken in an album, by fingerprint: those
// of every row, and those a plan gives.
type occurrences struct {
	taken map[occurrence]bool
	// from is, for a fingerprint, a number below which no occurrence is
	// free: the search does not start again from 1 for every twin.
	from map[string]int
}

type occurrence struct {
	fingerprint string
	n           int
}

func newOccurrences(olds []OldTrack) *occurrences {
	o := &occurrences{taken: map[occurrence]bool{}, from: map[string]int{}}
	for _, row := range olds {
		o.taken[occurrence{row.Fingerprint, row.Occurrence}] = true
	}
	return o
}

// next takes the lowest free occurrence above zero of a fingerprint.
func (o *occurrences) next(fingerprint string) int {
	n := max(o.from[fingerprint], 1)
	for o.taken[occurrence{fingerprint, n}] {
		n++
	}
	o.taken[occurrence{fingerprint, n}] = true
	o.from[fingerprint] = n + 1
	return n
}
