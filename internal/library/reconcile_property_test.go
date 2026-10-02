package library

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The properties of the planner (DESIGN.md §5.4, §12.1), checked on
// thousands of albums made at random from a fixed seed: a failure names the
// seed, and the same album is made again.

// verifyPlan checks that a plan is one the rules F1 to F5 allow for those
// rows and files, without computing it again:
//
//   - every file is in exactly one match or one insert, no row is in two
//     matches, and the rows that are gone are exactly the available rows
//     without a file: no row is paired with two files, and none is lost;
//   - each pair is one its phase allows: F1 the same SHA-256, F2 the same
//     fingerprint, of whatever version, F3 the same disc, number, codec
//     and known duration on a row of another version, which takes another
//     fingerprint;
//   - no phase left a pair it could make to the phases after it; F2 paired
//     no row that is not available while it left an available one with
//     that fingerprint, and in each of its two rounds it made the pairs in
//     one place before the others;
//   - after the plan, (fingerprint, occurrence) names one row of the album,
//     and a new occurrence is the lowest that was free.
func verifyPlan(olds []OldTrack, news []NewFile, plan Plan, version string) error {
	fileOf := make([]int, len(olds))    // the file of each row, or -1
	phaseOf := make([]Phase, len(olds)) // the phase that paired each row
	rowOf := make([]int, len(news))     // the row of each file, -1 for none, -2 for a new row
	uses := make([]int, len(news))      // how many times the plan names each file
	for i := range fileOf {
		fileOf[i] = -1
	}
	for j := range rowOf {
		rowOf[j] = -1
	}

	// What (fingerprint, occurrence) the rows had, and what the plan gives.
	had := map[occurrence]bool{}
	for _, o := range olds {
		had[occurrence{o.Fingerprint, o.Occurrence}] = true
	}
	var given []occurrence

	for _, m := range plan.Matches {
		if m.Old < 0 || m.Old >= len(olds) || m.New < 0 || m.New >= len(news) {
			return fmt.Errorf("the match %+v names a row or a file that does not exist", m)
		}
		o, n := olds[m.Old], news[m.New]
		if fileOf[m.Old] != -1 {
			return fmt.Errorf("the row %s is paired with two files", o.ID)
		}
		fileOf[m.Old], phaseOf[m.Old], rowOf[m.New] = m.New, m.Phase, m.Old
		uses[m.New]++
		keeps := m.Occurrence == o.Occurrence
		switch m.Phase {
		case PhaseContent:
			if o.FileSHA256 != n.FileSHA256 || !keeps {
				return fmt.Errorf("F1 paired the row %s with %q: another SHA-256, or another occurrence", o.ID, n.RelPath)
			}
		case PhaseFingerprint:
			if o.Fingerprint != n.Fingerprint || !keeps {
				return fmt.Errorf("F2 paired the row %s with %q: another fingerprint or occurrence", o.ID, n.RelPath)
			}
		case PhaseWeak:
			if o.FPVersion == version {
				return fmt.Errorf("F3 paired the row %s, whose fingerprint has the current version", o.ID)
			}
			if !sameSlot(o, n) {
				return fmt.Errorf("F3 paired the row %s with %q: another disc, number, codec or duration", o.ID, n.RelPath)
			}
			if o.Fingerprint == n.Fingerprint {
				return fmt.Errorf("F3 paired the row %s with %q, which has its fingerprint: F2 pairs those", o.ID, n.RelPath)
			}
			given = append(given, occurrence{n.Fingerprint, m.Occurrence})
		default:
			return fmt.Errorf("the match %+v has no phase", m)
		}
		if m.Phase != PhaseContent && n.Fingerprint == "" {
			return fmt.Errorf("%s paired %q, which has no fingerprint", phaseName(m.Phase), n.RelPath)
		}
	}
	for _, ins := range plan.Inserts {
		if ins.New < 0 || ins.New >= len(news) {
			return fmt.Errorf("the insert %+v names a file that does not exist", ins)
		}
		if news[ins.New].Fingerprint == "" {
			return fmt.Errorf("the new row of %q has no fingerprint", news[ins.New].RelPath)
		}
		rowOf[ins.New] = -2
		uses[ins.New]++
		given = append(given, occurrence{news[ins.New].Fingerprint, ins.Occurrence})
	}
	for j, n := range uses {
		if n != 1 {
			return fmt.Errorf("the file %q is in %d matches and inserts, want 1", news[j].RelPath, n)
		}
	}

	var gone []int
	for i, o := range olds {
		if o.Available && fileOf[i] == -1 {
			gone = append(gone, i)
		}
	}
	if !slices.Equal(plan.Gone, gone) {
		return fmt.Errorf("the rows that are gone are %v, want the available rows without a file, %v", plan.Gone, gone)
	}

	// No phase leaves to the next ones a pair it could make.
	for i, o := range olds {
		for j, n := range news {
			rowAfterF1 := fileOf[i] == -1 || phaseOf[i] != PhaseContent
			fileAfterF1 := rowOf[j] < 0 || phaseOf[rowOf[j]] != PhaseContent
			if rowAfterF1 && fileAfterF1 && o.FileSHA256 == n.FileSHA256 {
				return fmt.Errorf("F1 did not pair the row %s and %q, which have one SHA-256", o.ID, n.RelPath)
			}
			rowAfterF2 := fileOf[i] == -1 || phaseOf[i] == PhaseWeak
			fileAfterF2 := rowOf[j] < 0 || phaseOf[rowOf[j]] == PhaseWeak
			if rowAfterF2 && fileAfterF2 && o.Fingerprint == n.Fingerprint {
				return fmt.Errorf("F2 did not pair the row %s and %q, which have one fingerprint", o.ID, n.RelPath)
			}
			if fileOf[i] == -1 && rowOf[j] < 0 && o.FPVersion != version && sameSlot(o, n) {
				return fmt.Errorf("F3 did not pair the row %s and %q, which are in one place", o.ID, n.RelPath)
			}
			// Each round of F2 first pairs those with one fingerprint in
			// one place. Of a row and a file that F1 left, with one
			// fingerprint and one (disc, no): the row is in such a pair, or
			// the file is in one with a row of the same round, or the file
			// went to a row of the round before, an available one.
			if rowAfterF1 && fileAfterF1 && o.Fingerprint == n.Fingerprint && o.Disc == n.Disc && o.No == n.No {
				rowInPlace := fileOf[i] >= 0 && phaseOf[i] == PhaseFingerprint && news[fileOf[i]].Disc == o.Disc && news[fileOf[i]].No == o.No
				fileFirst := false
				if k := rowOf[j]; k >= 0 && phaseOf[k] == PhaseFingerprint {
					inPlace := olds[k].Disc == n.Disc && olds[k].No == n.No
					fileFirst = (olds[k].Available && !o.Available) || (inPlace && olds[k].Available == o.Available)
				}
				if !rowInPlace && !fileFirst {
					return fmt.Errorf("F2 did not pair first the row %s and %q, which have one fingerprint and one place", o.ID, n.RelPath)
				}
			}
		}
	}

	// F2 pairs the available rows of a fingerprint before the others.
	late := map[string]string{} // by fingerprint, a row that was not available and that F2 paired
	for i, o := range olds {
		if !o.Available && fileOf[i] >= 0 && phaseOf[i] == PhaseFingerprint {
			late[o.Fingerprint] = o.ID
		}
	}
	for i, o := range olds {
		other, ok := late[o.Fingerprint]
		if ok && o.Available && (fileOf[i] == -1 || phaseOf[i] == PhaseWeak) {
			return fmt.Errorf("F2 paired the row %s, which was not available, and left the row %s, which was and has its fingerprint", other, o.ID)
		}
	}

	// The occurrences the plan gives: above zero, never one a row had, never
	// one twice, and with no free one below them.
	taken := map[occurrence]bool{}
	for _, g := range given {
		if g.n < 1 || had[g] || taken[g] {
			return fmt.Errorf("the plan gives the occurrence %d of %q, which is below 1 or taken", g.n, g.fingerprint)
		}
		taken[g] = true
	}
	for _, g := range given {
		for n := 1; n < g.n; n++ {
			if below := (occurrence{g.fingerprint, n}); !had[below] && !taken[below] {
				return fmt.Errorf("the plan gives the occurrence %d of %q, and %d is free", g.n, g.fingerprint, n)
			}
		}
	}
	return nil
}

// sameSlot is the condition of F3.
func sameSlot(o OldTrack, n NewFile) bool {
	return o.Disc == n.Disc && o.No == n.No && o.Codec == n.Codec &&
		o.DurationMS != nil && n.DurationMS != nil && *o.DurationMS == *n.DurationMS
}

// reference computes the plan again, as the erratum of DESIGN.md §5.4
// writes the rules and with no code of the planner: a step makes one pair
// at a time, of the first file in its order that a free row can take with
// the first such row. It is what the plans of the random albums are
// compared with, so that no order of the planner can change unseen.
func reference(olds []OldTrack, news []NewFile, version string) outcome {
	rowOf := make([]int, len(news))
	fileOf := make([]int, len(olds))
	phase := make([]Phase, len(news))
	for j := range rowOf {
		rowOf[j] = -1
	}
	for i := range fileOf {
		fileOf[i] = -1
	}

	pathFirst := func(a, b NewFile) bool { return a.RelPath < b.RelPath }
	placeFirst := func(a, b NewFile) bool {
		if a.Disc != b.Disc {
			return a.Disc < b.Disc
		}
		if a.No != b.No {
			return a.No < b.No
		}
		return a.RelPath < b.RelPath
	}
	occurrenceFirst := func(a, b OldTrack) bool {
		if a.Occurrence != b.Occurrence {
			return a.Occurrence < b.Occurrence
		}
		return a.ID < b.ID
	}
	onDiskFirst := func(a, b OldTrack) bool {
		if a.Available != b.Available {
			return a.Available
		}
		if a.RelPath != b.RelPath {
			return a.RelPath < b.RelPath
		}
		return a.ID < b.ID
	}

	step := func(ph Phase, can func(o OldTrack, n NewFile) bool, fileFirst func(a, b NewFile) bool, rowFirst func(a, b OldTrack) bool) {
		for {
			file, row := -1, -1
			for j, n := range news {
				if rowOf[j] != -1 || (file != -1 && !fileFirst(n, news[file])) {
					continue
				}
				best := -1
				for i, o := range olds {
					if fileOf[i] == -1 && can(o, n) && (best == -1 || rowFirst(o, olds[best])) {
						best = i
					}
				}
				if best != -1 {
					file, row = j, best
				}
			}
			if file == -1 {
				return
			}
			rowOf[file], fileOf[row], phase[file] = row, file, ph
		}
	}

	// F1.
	step(PhaseContent, func(o OldTrack, n NewFile) bool { return o.FileSHA256 == n.FileSHA256 }, pathFirst, onDiskFirst)
	// F2: the available rows, then the others; each time the pairs in one
	// place, then the rest.
	for _, available := range []bool{true, false} {
		step(PhaseFingerprint, func(o OldTrack, n NewFile) bool {
			return o.Available == available && o.Fingerprint == n.Fingerprint && o.Disc == n.Disc && o.No == n.No
		}, placeFirst, occurrenceFirst)
		step(PhaseFingerprint, func(o OldTrack, n NewFile) bool {
			return o.Available == available && o.Fingerprint == n.Fingerprint
		}, placeFirst, occurrenceFirst)
	}
	// F3.
	step(PhaseWeak, func(o OldTrack, n NewFile) bool { return o.FPVersion != version && sameSlot(o, n) }, placeFirst, onDiskFirst)

	// F4, and the occurrences of the rows of F3.
	taken := map[occurrence]bool{}
	for _, o := range olds {
		taken[occurrence{o.Fingerprint, o.Occurrence}] = true
	}
	lowest := func(fingerprint string) int {
		n := 1
		for taken[occurrence{fingerprint, n}] {
			n++
		}
		taken[occurrence{fingerprint, n}] = true
		return n
	}
	order := make([]int, len(news))
	for j := range order {
		order[j] = j
	}
	slices.SortFunc(order, func(a, b int) int {
		switch {
		case placeFirst(news[a], news[b]):
			return -1
		case placeFirst(news[b], news[a]):
			return 1
		}
		return 0
	})
	out := outcome{files: map[string]string{}}
	for _, j := range order {
		n := news[j]
		switch i := rowOf[j]; {
		case i == -1:
			out.files[n.RelPath] = fmt.Sprintf("new #%d", lowest(n.Fingerprint))
		case phase[j] == PhaseWeak:
			out.files[n.RelPath] = fmt.Sprintf("%s F3 #%d", olds[i].ID, lowest(n.Fingerprint))
		default:
			out.files[n.RelPath] = fmt.Sprintf("%s %s #%d", olds[i].ID, phaseName(phase[j]), olds[i].Occurrence)
		}
	}
	// F5.
	for i, o := range olds {
		if o.Available && fileOf[i] == -1 {
			out.gone = append(out.gone, o.ID)
		}
	}
	slices.Sort(out.gone)
	return out
}

// ids gives the ids of the new rows: "id-1", "id-2", and so on.
type ids struct{ n int }

func newIDs() *ids { return &ids{} }

func (g *ids) next() string {
	g.n++
	return fmt.Sprintf("id-%d", g.n)
}

// applyPlan is what the indexer does with a plan, on rows in memory: a
// matched row takes the fields of its file and is available, and after F2
// and F3 also its fingerprint, with the current version; a row that is
// gone is not available; a new row is written by its natural key,
// (fingerprint, occurrence), as an upsert. No row is removed.
func applyPlan(olds []OldTrack, news []NewFile, plan Plan, version string, nextID func() string) []OldTrack {
	rows := slices.Clone(olds)
	for _, m := range plan.Matches {
		r, n := &rows[m.Old], news[m.New]
		r.RelPath, r.FileSHA256, r.Disc, r.No, r.DurationMS, r.Codec = n.RelPath, n.FileSHA256, n.Disc, n.No, n.DurationMS, n.Codec
		r.Available, r.Occurrence = true, m.Occurrence
		if m.Phase != PhaseContent {
			r.Fingerprint, r.FPVersion = n.Fingerprint, version
		}
	}
	for _, i := range plan.Gone {
		rows[i].Available = false
	}
	for _, ins := range plan.Inserts {
		n := news[ins.New]
		r := OldTrack{
			RelPath: n.RelPath, FileSHA256: n.FileSHA256, Disc: n.Disc, No: n.No, DurationMS: n.DurationMS, Codec: n.Codec,
			Fingerprint: n.Fingerprint, FPVersion: version, Occurrence: ins.Occurrence, Available: true,
		}
		at := slices.IndexFunc(rows, func(o OldTrack) bool {
			return o.Fingerprint == r.Fingerprint && o.Occurrence == r.Occurrence
		})
		if at >= 0 {
			r.ID = rows[at].ID
			rows[at] = r
		} else {
			r.ID = nextID()
			rows = append(rows, r)
		}
	}
	return rows
}

// albums makes rows and files at random. The values come from a few, so
// that every rule meets its hard cases often: tracks with one audio, files
// with one SHA-256, rows of the version before, rows that are not
// available, places shared by a row and a file.
type albums struct {
	rng *rand.Rand
	// stable is true when the current ffmpeg computes the fingerprints the
	// one before computed: the same text, with another version.
	stable bool
	shas   int
}

func newAlbums(seed uint64) *albums {
	rng := rand.New(rand.NewPCG(seed, 6))
	return &albums{rng: rng, stable: rng.IntN(2) == 0}
}

func (g *albums) chance(p float64) bool { return g.rng.Float64() < p }

func (g *albums) fingerprint(version string, audio int) string {
	if g.stable || version == fpNow {
		return fmt.Sprintf("fp-%d", audio)
	}
	return fmt.Sprintf("old-%d", audio)
}

func (g *albums) sha() string {
	g.shas++
	return fmt.Sprintf("sha-%d", g.shas)
}

func (g *albums) duration() *int64 {
	if g.chance(0.15) {
		return nil
	}
	return msec(int64(1000 * (1 + g.rng.IntN(2))))
}

func (g *albums) codec() string { return []string{"flac", "mp3"}[g.rng.IntN(2)] }

// album returns the rows of an album, in a state the index can be in, and
// the files of its new receipt, each with the fingerprint of its audio.
func (g *albums) album() ([]OldTrack, []NewFile) {
	var olds []OldTrack
	var audios []int
	taken := map[occurrence]bool{}
	for i := range g.rng.IntN(8) {
		audio := g.rng.IntN(5)
		version := fpNow
		if g.chance(0.3) {
			version = fpBefore
		}
		o := OldTrack{
			ID:          fmt.Sprintf("t%d", i),
			RelPath:     fmt.Sprintf("p%d", i),
			FileSHA256:  g.sha(),
			Disc:        1 + g.rng.IntN(2),
			No:          1 + g.rng.IntN(4),
			DurationMS:  g.duration(),
			Codec:       g.codec(),
			Fingerprint: g.fingerprint(version, audio),
			FPVersion:   version,
			Available:   g.chance(0.7),
		}
		// A free occurrence of the fingerprint, not always the lowest: rows
		// may have left gaps.
		for o.Occurrence = 1 + g.rng.IntN(2); taken[occurrence{o.Fingerprint, o.Occurrence}]; o.Occurrence++ {
		}
		taken[occurrence{o.Fingerprint, o.Occurrence}] = true
		if i > 0 {
			other := olds[g.rng.IntN(i)]
			if g.chance(0.1) {
				o.FileSHA256 = other.FileSHA256
			}
			if !o.Available && g.chance(0.3) {
				// What was in that place before the row that is there now.
				o.RelPath, o.Disc, o.No = other.RelPath, other.Disc, other.No
			}
		}
		olds = append(olds, o)
		audios = append(audios, audio)
	}

	var news []NewFile
	paths := map[string]bool{}
	add := func(n NewFile) {
		for paths[n.RelPath] {
			n.RelPath += "'"
		}
		paths[n.RelPath] = true
		news = append(news, n)
	}
	for i, o := range olds {
		n := NewFile{RelPath: o.RelPath, FileSHA256: o.FileSHA256, Disc: o.Disc, No: o.No, DurationMS: o.DurationMS, Codec: o.Codec,
			Fingerprint: g.fingerprint(fpNow, audios[i])}
		switch r := g.rng.Float64(); {
		case r < 0.30: // the file as it was
		case r < 0.60: // rewritten: other tags, the same audio
			n.FileSHA256 = g.sha()
			if g.chance(0.4) {
				n.Disc, n.No = 1+g.rng.IntN(2), 1+g.rng.IntN(4)
			}
			if g.chance(0.4) {
				n.RelPath = fmt.Sprintf("q%d", i)
			}
		case r < 0.70: // another audio in its place
			n.FileSHA256 = g.sha()
			n.Fingerprint = g.fingerprint(fpNow, 5+g.rng.IntN(3))
			if g.chance(0.5) {
				n.DurationMS = g.duration()
			}
		default: // no file any more
			continue
		}
		add(n)
	}
	for i := range g.rng.IntN(3) {
		n := NewFile{RelPath: fmt.Sprintf("n%d", i), FileSHA256: g.sha(), Disc: 1 + g.rng.IntN(2), No: 1 + g.rng.IntN(4),
			DurationMS: g.duration(), Codec: g.codec(), Fingerprint: g.fingerprint(fpNow, g.rng.IntN(8))}
		if len(olds) > 0 && g.chance(0.15) {
			n.FileSHA256 = olds[g.rng.IntN(len(olds))].FileSHA256
		}
		add(n)
	}
	g.rng.Shuffle(len(news), func(i, j int) { news[i], news[j] = news[j], news[i] })
	return olds, news
}

func showCase(olds []OldTrack, news []NewFile) string {
	var b strings.Builder
	show := func(d *int64) string {
		if d == nil {
			return "?"
		}
		return fmt.Sprint(*d)
	}
	for _, o := range olds {
		fmt.Fprintf(&b, "  row  %-3s %d-%d %-4s %-7s %-4s %sms  %s@%s #%d available=%t\n",
			o.ID, o.Disc, o.No, o.RelPath, o.FileSHA256, o.Codec, show(o.DurationMS), o.Fingerprint, o.FPVersion, o.Occurrence, o.Available)
	}
	for _, n := range news {
		fmt.Fprintf(&b, "  file     %d-%d %-4s %-7s %-4s %sms  %s\n",
			n.Disc, n.No, n.RelPath, n.FileSHA256, n.Codec, show(n.DurationMS), n.Fingerprint)
	}
	return b.String()
}

// checkAlbum checks every property of the planner on one album, and
// returns the plan.
func checkAlbum(rng *rand.Rand, olds []OldTrack, news []NewFile) (Plan, error) {
	plan, err := Reconcile(olds, news, fpNow)
	if err != nil {
		return plan, fmt.Errorf("Reconcile: %w", err)
	}
	if err := verifyPlan(olds, news, plan, fpNow); err != nil {
		return plan, err
	}
	if got, want := describe(olds, news, plan), reference(olds, news, fpNow); !reflect.DeepEqual(got, want) {
		return plan, fmt.Errorf("the plan is\n%s\nand the rules, one pair at a time, give\n%s", got, want)
	}

	// NeedFingerprint names the files F1 does not pair, and Reconcile needs
	// the fingerprint of those and of no other.
	need := NeedFingerprint(olds, news)
	var unpaired []int
	paired := map[int]bool{}
	for _, m := range plan.Matches {
		if m.Phase == PhaseContent {
			paired[m.New] = true
		}
	}
	for j := range news {
		if !paired[j] {
			unpaired = append(unpaired, j)
		}
	}
	if !slices.Equal(need, unpaired) {
		return plan, fmt.Errorf("NeedFingerprint = %v, and the files F1 does not pair are %v", need, unpaired)
	}
	// It names them from what the index and the receipt say alone: the
	// SHA-256, the paths, and which rows are available.
	indexed, listed := make([]OldTrack, len(olds)), make([]NewFile, len(news))
	for i, o := range olds {
		indexed[i] = OldTrack{ID: o.ID, RelPath: o.RelPath, FileSHA256: o.FileSHA256, Available: o.Available}
	}
	for j, n := range news {
		listed[j] = NewFile{RelPath: n.RelPath, FileSHA256: n.FileSHA256}
	}
	if got := NeedFingerprint(indexed, listed); !slices.Equal(got, need) {
		return plan, fmt.Errorf("NeedFingerprint = %v before the files are examined, and %v after", got, need)
	}
	needed := withFingerprints(news, need)
	if got, err := Reconcile(olds, needed, fpNow); err != nil || !reflect.DeepEqual(got, plan) {
		return plan, fmt.Errorf("with only the fingerprints NeedFingerprint names the plan is %+v (%v), want %+v", got, err, plan)
	}
	for _, j := range need {
		missing := slices.Clone(needed)
		missing[j].Fingerprint = ""
		if _, err := Reconcile(olds, missing, fpNow); err == nil {
			return plan, fmt.Errorf("Reconcile accepted %q without its fingerprint", news[j].RelPath)
		}
	}

	// The plan does not depend on the order of the rows and of the files.
	shuffledOlds, shuffledNews := slices.Clone(olds), slices.Clone(news)
	rng.Shuffle(len(shuffledOlds), func(i, j int) { shuffledOlds[i], shuffledOlds[j] = shuffledOlds[j], shuffledOlds[i] })
	rng.Shuffle(len(shuffledNews), func(i, j int) { shuffledNews[i], shuffledNews[j] = shuffledNews[j], shuffledNews[i] })
	shuffled, err := Reconcile(shuffledOlds, shuffledNews, fpNow)
	if err != nil {
		return plan, fmt.Errorf("Reconcile on the same album in another order: %w", err)
	}
	if got, want := describe(shuffledOlds, shuffledNews, shuffled), describe(olds, news, plan); !reflect.DeepEqual(got, want) {
		return plan, fmt.Errorf("with the rows and the files in another order the plan is\n%s\nwant\n%s", got, want)
	}

	// Applied, the plan removes no row and changes no id; the available
	// rows are the files; (fingerprint, occurrence) names one row.
	applied := applyPlan(olds, news, plan, fpNow, newIDs().next)
	if len(applied) != len(olds)+len(plan.Inserts) {
		return plan, fmt.Errorf("%d rows and %d new ones became %d rows", len(olds), len(plan.Inserts), len(applied))
	}
	touched := map[int]bool{}
	for _, m := range plan.Matches {
		touched[m.Old] = true
	}
	for _, i := range plan.Gone {
		touched[i] = true
	}
	for i, o := range olds {
		if applied[i].ID != o.ID {
			return plan, fmt.Errorf("the row %s has the id %s after the plan", o.ID, applied[i].ID)
		}
		if !touched[i] && !reflect.DeepEqual(applied[i], o) {
			return plan, fmt.Errorf("the row %s, which is in no match and is not gone, changed", o.ID)
		}
	}
	rowAt := map[string]OldTrack{}
	keys := map[occurrence]string{}
	seen := map[string]bool{}
	for _, r := range applied {
		if seen[r.ID] {
			return plan, fmt.Errorf("two rows have the id %s after the plan", r.ID)
		}
		seen[r.ID] = true
		key := occurrence{r.Fingerprint, r.Occurrence}
		if other, ok := keys[key]; ok || r.Occurrence < 1 {
			return plan, fmt.Errorf("the rows %s and %s have the fingerprint %q and the occurrence %d", other, r.ID, r.Fingerprint, r.Occurrence)
		}
		keys[key] = r.ID
		if r.Available {
			if other, ok := rowAt[r.RelPath]; ok {
				return plan, fmt.Errorf("the rows %s and %s are both the file %q", other.ID, r.ID, r.RelPath)
			}
			rowAt[r.RelPath] = r
		}
	}
	if len(rowAt) != len(news) {
		return plan, fmt.Errorf("%d rows are available for %d files", len(rowAt), len(news))
	}
	for _, n := range news {
		r, ok := rowAt[n.RelPath]
		if !ok || r.FileSHA256 != n.FileSHA256 || r.Disc != n.Disc || r.No != n.No || r.Codec != n.Codec || r.DurationMS != n.DurationMS {
			return plan, fmt.Errorf("no available row has the fields of the file %q", n.RelPath)
		}
	}

	// Applying the same plan again changes nothing.
	if again := applyPlan(applied, news, plan, fpNow, newIDs().next); !reflect.DeepEqual(again, applied) {
		return plan, fmt.Errorf("the plan applied twice gives\n%+v\nand applied once\n%+v", again, applied)
	}

	// Scanning the same files again examines nothing and changes nothing,
	// even where several rows have one SHA-256: every row has its file
	// again.
	if need := NeedFingerprint(applied, news); len(need) != 0 {
		return plan, fmt.Errorf("after the plan, NeedFingerprint still names %v", need)
	}
	second, err := Reconcile(applied, withFingerprints(news, nil), fpNow)
	if err != nil {
		return plan, fmt.Errorf("Reconcile after the plan: %w", err)
	}
	if err := verifyPlan(applied, withFingerprints(news, nil), second, fpNow); err != nil {
		return plan, fmt.Errorf("the second plan: %w", err)
	}
	if len(second.Inserts) != 0 || len(second.Matches) != len(news) {
		return plan, fmt.Errorf("the second plan has %d new rows and %d matches for %d files", len(second.Inserts), len(second.Matches), len(news))
	}
	if len(second.Gone) != 0 {
		return plan, fmt.Errorf("the second plan makes the rows %v unavailable", second.Gone)
	}
	if again := applyPlan(applied, news, second, fpNow, newIDs().next); !reflect.DeepEqual(again, applied) {
		return plan, fmt.Errorf("the second plan changes the rows:\n%+v\nwere\n%+v", again, applied)
	}
	return plan, nil
}

func TestReconcileProperties(t *testing.T) {
	var content, audio, otherVersion, audioBack, weak, rekeyed, inserts, gone, back, sharedSHA int
	for seed := range uint64(30000) {
		g := newAlbums(seed)
		olds, news := g.album()
		plan, err := checkAlbum(g.rng, olds, news)
		if err != nil {
			t.Fatalf("seed %d: %v\n%s", seed, err, showCase(olds, news))
		}
		for _, m := range plan.Matches {
			switch m.Phase {
			case PhaseContent:
				content++
			case PhaseFingerprint:
				audio++
				if olds[m.Old].FPVersion != fpNow {
					otherVersion++
				}
				if !olds[m.Old].Available {
					audioBack++
				}
			case PhaseWeak:
				weak++
				if m.Occurrence != olds[m.Old].Occurrence {
					rekeyed++
				}
			}
			if !olds[m.Old].Available {
				back++
			}
		}
		inserts += len(plan.Inserts)
		gone += len(plan.Gone)
		shas := map[string]bool{}
		for _, o := range olds {
			if shas[o.FileSHA256] {
				sharedSHA++
			}
			shas[o.FileSHA256] = true
		}
	}
	// The generator must reach every rule, or the properties say nothing.
	for name, n := range map[string]int{
		"F1 matches": content, "F2 matches": audio, "F2 matches of a row of another version": otherVersion,
		"F2 matches of a row that was not available": audioBack,
		"F3 matches": weak, "F3 matches that change the occurrence": rekeyed,
		"new rows": inserts, "rows that are gone": gone, "rows that come back": back, "rows that share a SHA-256": sharedSHA,
	} {
		if n < 300 {
			t.Errorf("only %d %s in all the albums: the generator does not reach the rule", n, name)
		}
	}
}

// diskTrack is a track file on disk in the tests that follow the audio: its
// audio is a number, and two tracks of an album never have the same.
type diskTrack struct {
	audio    int
	disc, no int
	path     string
	sha      string
}

// model is an album as MusicLib changes it and the indexer follows it:
// the files on disk, the rows of the index, and the audio each row was
// created for, which must be the audio of its file for ever (D6).
type model struct {
	t       *testing.T
	rng     *rand.Rand
	seed    uint64
	disk    []diskTrack
	deleted []int // audios that were on disk, and may come back
	rows    []OldTrack
	audioOf map[string]int // the audio of each row, by its id
	ids     *ids
	audios  int
	shas    int
	log     []string
	// stable is true when every ffmpeg computes the same fingerprint of an
	// audio; if it is false each version computes its own.
	stable bool
}

func newModel(t *testing.T, seed uint64) *model {
	return &model{t: t, rng: rand.New(rand.NewPCG(seed, 7)), seed: seed, audioOf: map[string]int{}, ids: newIDs()}
}

func (l *model) failf(format string, args ...any) {
	l.t.Helper()
	l.t.Fatalf("seed %d: %s\nsteps:\n  %s\nrows:\n%s", l.seed, fmt.Sprintf(format, args...),
		strings.Join(l.log, "\n  "), showCase(l.rows, nil))
}

func (l *model) chance(p float64) bool { return l.rng.Float64() < p }

func (l *model) newSHA() string {
	l.shas++
	return fmt.Sprintf("sha-%d", l.shas)
}

// freePlace returns a (disc, no) that no file on disk has.
func (l *model) freePlace() (int, int) {
	for {
		disc, no := 1+l.rng.IntN(2), 1+l.rng.IntN(40)
		if !slices.ContainsFunc(l.disk, func(f diskTrack) bool { return f.disc == disc && f.no == no }) {
			return disc, no
		}
	}
}

// write rewrites the file of a track as MusicLib does after a change of
// its tags: other bytes, a name from its number, the same audio.
func (l *model) write(f *diskTrack) {
	f.sha = l.newSHA()
	f.path = fmt.Sprintf("Disc %d/%02d - audio %d (%s).flac", f.disc, f.no, f.audio, f.sha)
}

func (l *model) add(audio int) {
	f := diskTrack{audio: audio}
	f.disc, f.no = l.freePlace()
	l.write(&f)
	l.disk = append(l.disk, f)
}

// The fingerprint and the duration of an audio. Each audio has a duration
// of its own, so that F3, which trusts the duration, cannot take one audio
// for another.
func fingerprintOf(version string, audio int) string {
	return fmt.Sprintf("%s:audio-%d", version, audio)
}

func (l *model) fingerprint(version string, audio int) string {
	if l.stable {
		return fingerprintOf("every version", audio)
	}
	return fingerprintOf(version, audio)
}
func durationOf(audio int) *int64 { return msec(int64(60000 + audio)) }

// edit changes the album as a user of MusicLib would. If keepPlaces is
// true no track changes its disc or its number.
func (l *model) edit(keepPlaces bool) {
	// Tracks are deleted.
	for i := len(l.disk) - 1; i >= 0; i-- {
		if l.chance(0.12) {
			l.log = append(l.log, fmt.Sprintf("delete audio %d", l.disk[i].audio))
			l.deleted = append(l.deleted, l.disk[i].audio)
			l.disk = slices.Delete(l.disk, i, i+1)
		}
	}
	// Tags change: the file is rewritten.
	for i := range l.disk {
		if l.chance(0.3) {
			l.log = append(l.log, fmt.Sprintf("retag audio %d", l.disk[i].audio))
			l.write(&l.disk[i])
		}
	}
	if !keepPlaces {
		// Two tracks swap their numbers.
		if len(l.disk) >= 2 && l.chance(0.4) {
			a, b := &l.disk[l.rng.IntN(len(l.disk))], &l.disk[l.rng.IntN(len(l.disk))]
			l.log = append(l.log, fmt.Sprintf("swap audio %d and audio %d", a.audio, b.audio))
			a.disc, a.no, b.disc, b.no = b.disc, b.no, a.disc, a.no
			l.write(a)
			l.write(b)
		}
		// A track moves to a free number.
		if len(l.disk) >= 1 && l.chance(0.3) {
			f := &l.disk[l.rng.IntN(len(l.disk))]
			f.disc, f.no = l.freePlace()
			l.log = append(l.log, fmt.Sprintf("move audio %d to %d-%d", f.audio, f.disc, f.no))
			l.write(f)
		}
		// A deleted track is imported again, anywhere.
		if len(l.deleted) > 0 && l.chance(0.4) {
			i := l.rng.IntN(len(l.deleted))
			l.log = append(l.log, fmt.Sprintf("import audio %d again", l.deleted[i]))
			l.add(l.deleted[i])
			l.deleted = slices.Delete(l.deleted, i, i+1)
		}
	}
	// New tracks.
	for range l.rng.IntN(3) {
		l.audios++
		l.log = append(l.log, fmt.Sprintf("add audio %d", l.audios))
		l.add(l.audios)
	}
}

// scan indexes the files on disk as the indexer does: it asks which files
// need a fingerprint, computes those and no other, reconciles and applies
// the plan. Then it checks that every row still is the audio it was
// created for.
func (l *model) scan(version string, files []diskTrack) {
	l.t.Helper()
	news := make([]NewFile, len(files))
	for j, f := range files {
		news[j] = NewFile{RelPath: f.path, FileSHA256: f.sha, Disc: f.disc, No: f.no, DurationMS: durationOf(f.audio), Codec: "flac"}
	}
	for _, j := range NeedFingerprint(l.rows, news) {
		news[j].Fingerprint = l.fingerprint(version, files[j].audio)
	}
	plan, err := Reconcile(l.rows, news, version)
	if err != nil {
		l.failf("Reconcile: %v", err)
	}
	if err := verifyPlan(l.rows, news, plan, version); err != nil {
		l.failf("the plan breaks a rule: %v", err)
	}
	l.log = append(l.log, fmt.Sprintf("scan %d files with ffmpeg %s: %d matches, %d new, %d gone",
		len(files), version, len(plan.Matches), len(plan.Inserts), len(plan.Gone)))
	before := len(l.rows)
	l.rows = applyPlan(l.rows, news, plan, version, l.ids.next)
	for k, ins := range plan.Inserts {
		l.audioOf[l.rows[before+k].ID] = files[ins.New].audio
	}

	onDisk := map[int]diskTrack{}
	for _, f := range files {
		onDisk[f.audio] = f
	}
	rowOf := map[int]string{}
	for _, r := range l.rows {
		audio, ok := l.audioOf[r.ID]
		if !ok {
			l.failf("the row %s has no audio: it was not created by a plan", r.ID)
		}
		if other, twice := rowOf[audio]; twice {
			l.failf("the rows %s and %s are both audio %d: a track got a second id", other, r.ID, audio)
		}
		rowOf[audio] = r.ID
		f, present := onDisk[audio]
		if r.Available != present {
			l.failf("the row %s of audio %d has available=%t, and the audio on disk: %t", r.ID, audio, r.Available, present)
		}
		if present && (r.RelPath != f.path || r.FileSHA256 != f.sha || r.Disc != f.disc || r.No != f.no) {
			l.failf("the row %s of audio %d is the file %q, and its audio is in %q", r.ID, audio, r.RelPath, f.path)
		}
	}
	for audio := range onDisk {
		if _, ok := rowOf[audio]; !ok {
			l.failf("audio %d is on disk and has no row", audio)
		}
	}
}

// The promise of D6, on albums that change at random for many scans: the
// id of a track follows its audio through changes of tags, of numbers and
// of names, a deletion makes its row unavailable, and the same audio
// imported again is the same track. No row is ever removed and no track
// ever gets a second id.
func TestIDsFollowTheAudio(t *testing.T) {
	for seed := range uint64(1500) {
		l := newModel(t, seed)
		for range 3 {
			l.audios++
			l.add(l.audios)
		}
		l.scan(fpNow, l.disk)
		for range 12 {
			l.edit(false)
			if l.chance(0.1) {
				// The album goes to the trash, or library/ cannot be read:
				// no file, and then the files again.
				l.log = append(l.log, "trash")
				l.scan(fpNow, nil)
			}
			l.scan(fpNow, l.disk)
			if l.chance(0.3) {
				// A render that changes no byte.
				l.scan(fpNow, l.disk)
			}
		}
	}
}

// The same promise when ffmpeg changes version and the album is modified
// before the fingerprints of its rows are computed again (F3): the tracks
// that stay in their place keep their ids, whether their row has the new
// fingerprint already or not.
func TestIDsFollowTheAudioWhenFFmpegChanges(t *testing.T) {
	for seed := range uint64(1500) {
		l := newModel(t, seed)
		for range 2 + l.rng.IntN(5) {
			l.audios++
			l.add(l.audios)
		}
		l.scan(fpBefore, l.disk)
		for range 2 {
			l.edit(false)
			l.scan(fpBefore, l.disk)
		}

		// The server starts with another ffmpeg. The job of §6.6 computes
		// the fingerprints again, on the same rows; it has reached some of
		// the available rows when MusicLib changes the album.
		l.log = append(l.log, "ffmpeg changes version")
		for i := range l.rows {
			if r := &l.rows[i]; r.Available && l.chance(0.4) {
				r.Fingerprint, r.FPVersion = l.fingerprint(fpNow, l.audioOf[r.ID]), fpNow
				l.log = append(l.log, fmt.Sprintf("the job computes the fingerprint of audio %d again", l.audioOf[r.ID]))
			}
		}
		l.edit(true)
		l.scan(fpNow, l.disk)
		// And a render that changes no byte.
		l.scan(fpNow, l.disk)
	}
}

// The promise of D6 when ffmpeg changes version and the new one computes
// the fingerprints the old one computed. The album changes in every way,
// and the tracks that move, and those that were deleted before the change
// and are imported again, keep their ids, whether the job of §6.6 reached
// their row or not: two equal fingerprints are the same audio.
func TestIDsFollowTheAudioWhenOnlyTheVersionOfFFmpegChanges(t *testing.T) {
	for seed := range uint64(1500) {
		l := newModel(t, seed)
		l.stable = true
		for range 2 + l.rng.IntN(5) {
			l.audios++
			l.add(l.audios)
		}
		l.scan(fpBefore, l.disk)
		for range 3 {
			l.edit(false)
			l.scan(fpBefore, l.disk)
		}

		l.log = append(l.log, "ffmpeg changes version")
		for i := range l.rows {
			if r := &l.rows[i]; r.Available && l.chance(0.4) {
				r.FPVersion = fpNow
				l.log = append(l.log, fmt.Sprintf("the job computes the fingerprint of audio %d again", l.audioOf[r.ID]))
			}
		}
		for range 6 {
			l.edit(false)
			if l.chance(0.1) {
				l.log = append(l.log, "trash")
				l.scan(fpNow, nil)
			}
			l.scan(fpNow, l.disk)
		}
	}
}

// twin is a file on disk in the test of the tracks with one audio.
type twin struct {
	audio    int
	disc, no int
	title    int
	path     string
	sha      string
	// row is the id of the row of the file, from the first scan that sees
	// it.
	row string
}

// twins is an album where several tracks have the same audio, as MusicLib
// changes it and the indexer follows it.
type twins struct {
	t       *testing.T
	rng     *rand.Rand
	seed    uint64
	disk    []twin
	rows    []OldTrack
	ids     *ids
	version string
	// peak is, for each audio, the most files that had it at one time.
	peak map[int]int
	shas int
	log  []string
}

func (w *twins) failf(format string, args ...any) {
	w.t.Helper()
	w.t.Fatalf("seed %d: %s\nsteps:\n  %s\nrows:\n%s", w.seed, fmt.Sprintf(format, args...),
		strings.Join(w.log, "\n  "), showCase(w.rows, nil))
}

// place returns a (disc, no) that no file on disk has. Half the times it
// is the place of a row that is not available, if there is one: the place
// where the rules could take a track for another.
func (w *twins) place() (int, int) {
	free := func(disc, no int) bool {
		return !slices.ContainsFunc(w.disk, func(f twin) bool { return f.disc == disc && f.no == no })
	}
	var left [][2]int
	for _, r := range w.rows {
		if !r.Available && free(r.Disc, r.No) {
			left = append(left, [2]int{r.Disc, r.No})
		}
	}
	if len(left) > 0 && w.rng.IntN(2) == 0 {
		at := left[w.rng.IntN(len(left))]
		return at[0], at[1]
	}
	for {
		if disc, no := 1+w.rng.IntN(2), 1+w.rng.IntN(12); free(disc, no) {
			return disc, no
		}
	}
}

// write rewrites a file as MusicLib does: other bytes, and half the times
// another title, so another name.
func (w *twins) write(f *twin) {
	w.shas++
	f.sha = fmt.Sprintf("sha-%d", w.shas)
	if w.rng.IntN(2) == 0 {
		f.title = w.shas
	}
	f.path = fmt.Sprintf("Disc %d/%02d - audio %d, title %d.flac", f.disc, f.no, f.audio, f.title)
}

// retag rewrites some of the files, where they are; never the file at the
// index except.
func (w *twins) retag(except int) {
	for i := range w.disk {
		if i != except && w.rng.IntN(3) == 0 {
			w.log = append(w.log, fmt.Sprintf("retag %d-%d", w.disk[i].disc, w.disk[i].no))
			w.write(&w.disk[i])
		}
	}
}

func (w *twins) add() {
	f := twin{audio: 1 + w.rng.IntN(2)}
	f.disc, f.no = w.place()
	w.write(&f)
	w.log = append(w.log, fmt.Sprintf("add a copy of audio %d at %d-%d", f.audio, f.disc, f.no))
	w.disk = append(w.disk, f)
}

// edit changes the album between two scans in one of the ways after which
// every track can still be told from its twins: tracks are deleted, or one
// track moves, or one is added, and the others stay where they are,
// rewritten or not.
func (w *twins) edit() {
	switch kind := w.rng.IntN(4); {
	case kind == 0 && len(w.disk) > 0:
		for i := len(w.disk) - 1; i >= 0; i-- {
			if w.rng.IntN(3) == 0 {
				w.log = append(w.log, fmt.Sprintf("delete %d-%d", w.disk[i].disc, w.disk[i].no))
				w.disk = slices.Delete(w.disk, i, i+1)
			}
		}
		w.retag(-1)
	case kind == 1 && len(w.disk) > 0:
		i := w.rng.IntN(len(w.disk))
		f := &w.disk[i]
		disc, no := w.place()
		w.log = append(w.log, fmt.Sprintf("move %d-%d to %d-%d", f.disc, f.no, disc, no))
		f.disc, f.no = disc, no
		w.write(f)
		w.retag(i)
	case kind == 2 && len(w.disk) < 8:
		w.retag(-1)
		w.add()
	default:
		w.retag(-1)
	}
}

// scan indexes the files on disk, or none if the album is in the trash, and
// checks that every file has the row it had, that a file seen for the
// first time took no row of a file on disk, and that no audio has more
// rows than it ever had files.
func (w *twins) scan(trash bool) {
	w.t.Helper()
	disk := w.disk
	if trash {
		disk = nil
	}
	news := make([]NewFile, len(disk))
	for j, f := range disk {
		news[j] = NewFile{RelPath: f.path, FileSHA256: f.sha, Disc: f.disc, No: f.no, DurationMS: durationOf(f.audio), Codec: "flac"}
	}
	for _, j := range NeedFingerprint(w.rows, news) {
		news[j].Fingerprint = fmt.Sprintf("audio-%d", disk[j].audio)
	}
	plan, err := Reconcile(w.rows, news, w.version)
	if err != nil {
		w.failf("Reconcile: %v", err)
	}
	if err := verifyPlan(w.rows, news, plan, w.version); err != nil {
		w.failf("the plan breaks a rule: %v", err)
	}
	wasAvailable := map[string]bool{}
	for _, r := range w.rows {
		wasAvailable[r.ID] = r.Available
	}
	w.rows = applyPlan(w.rows, news, plan, w.version, w.ids.next)
	w.log = append(w.log, fmt.Sprintf("scan %d files with ffmpeg %s: %d matches, %d new, %d gone",
		len(disk), w.version, len(plan.Matches), len(plan.Inserts), len(plan.Gone)))

	rowAt := map[string]OldTrack{}
	rowsOf := map[string]int{}
	for _, r := range w.rows {
		rowsOf[r.Fingerprint]++
		if r.Available {
			rowAt[r.RelPath] = r
		}
	}
	if len(rowAt) != len(disk) {
		w.failf("%d rows are available for %d files", len(rowAt), len(disk))
	}
	files := map[int]int{}
	for j := range disk {
		f := &disk[j]
		files[f.audio]++
		r, ok := rowAt[f.path]
		switch {
		case !ok:
			w.failf("the file %q has no available row", f.path)
		case f.row == "" && wasAvailable[r.ID]:
			w.failf("the new file %q took the row %s, which was the track of a file on disk", f.path, r.ID)
		case f.row != "" && f.row != r.ID:
			w.failf("the track %s, now in %q, became the track %s", f.row, f.path, r.ID)
		}
		f.row = r.ID
	}
	for audio, n := range files {
		w.peak[audio] = max(w.peak[audio], n)
	}
	for audio, n := range w.peak {
		if got := rowsOf[fmt.Sprintf("audio-%d", audio)]; got != n {
			w.failf("audio %d has %d rows, and never had more than %d files at a time", audio, got, n)
		}
	}
}

// Two tracks with the same audio are told apart only by their places and
// by what the index remembers of them. As long as the scanner sees a
// deletion, a move and an import one at a time, that is enough: a track
// keeps its id when it moves, also to the number of a deleted twin and
// also after ffmpeg changed; the row of a deleted track waits, and takes a
// file only when a copy more is imported.
func TestTwinsKeepTheirIDs(t *testing.T) {
	for seed := range uint64(3000) {
		w := &twins{t: t, rng: rand.New(rand.NewPCG(seed, 8)), seed: seed, ids: newIDs(), version: fpBefore, peak: map[int]int{}}
		for range 2 + w.rng.IntN(3) {
			w.add()
		}
		w.scan(false)
		changes := 2 + w.rng.IntN(12)
		for step := range 14 {
			if step == changes {
				w.log = append(w.log, "ffmpeg changes version")
				w.version = fpNow
			}
			w.edit()
			w.scan(false)
			if w.rng.IntN(8) == 0 {
				// The trash and the restore, which changes no byte.
				w.log = append(w.log, "trash")
				w.scan(true)
				w.scan(false)
			}
		}
	}
}
