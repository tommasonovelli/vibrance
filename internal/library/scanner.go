package library

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"vibrance/internal/store"
)

// State is what the scanner is doing, or why it does nothing (DESIGN.md
// §6.1).
type State string

const (
	// StateIdle: no cycle is running.
	StateIdle State = "idle"
	// StateScanning: a cycle is running.
	StateScanning State = "scanning"
	// StateMaintenance: the marker .maintenance exists. MusicLib is
	// rebuilding or restoring its library, which may be empty or
	// incomplete: the index is left as it is (§4.5).
	StateMaintenance State = "maintenance"
	// StateUnavailable: the marker .musiclib-store is missing, so the folder
	// is not MusicLib's data volume, or library/ cannot be listed. The
	// index is left as it is.
	StateUnavailable State = "unavailable"
)

// Reason is why a cycle runs (§6.1). It is only logged.
type Reason string

const (
	// ReasonStartup: the server started.
	ReasonStartup Reason = "startup"
	// ReasonInterval: VIBRANCE_SCAN_INTERVAL passed since the last cycle.
	ReasonInterval Reason = "interval"
	// ReasonRequest: an admin asked for a scan.
	ReasonRequest Reason = "request"
	// ReasonFileReplaced: a media endpoint found a file that is not the one
	// the index describes (§9.1).
	ReasonFileReplaced Reason = "file_replaced"
)

// MaxProblems is how many problems the status lists at most (§6.5).
const MaxProblems = 200

// optimizeAfter is how many albums a cycle must have written, or found
// gone, for SQLite to be asked to refresh its statistics at the end of the
// cycle (P6, T30): the first scan of a library, or a library that comes
// back, not the edit of an album.
const optimizeAfter = 100

// Scan is one cycle that went through the library.
type Scan struct {
	StartedAt time.Time
	// FinishedAt is nil while the cycle runs.
	FinishedAt *time.Time
	// OK is true when the cycle went to its end: the problems of single
	// albums do not make it false.
	OK bool
	// Error says why a finished cycle is not OK; "" otherwise. It never
	// holds the details of an internal failure, which are in the log.
	Error string
}

// Counts are how many rows of a table are available and how many are not.
type Counts struct {
	Available   int64
	Unavailable int64
}

// Progress is how far the cycle that is running has come.
type Progress struct {
	// Discovered are the album folders with a valid receipt.
	Discovered int
	// Pending are the albums still to index in this cycle.
	Pending int
	// Indexed are the albums this cycle has indexed.
	Indexed int
}

// Status is the state of the library as the scanner knows it (§6.5).
type Status struct {
	State State
	// LastScan is the cycle that is running or, when none is, the last one
	// that went through the library; nil before the first.
	LastScan *Scan
	// Albums and Tracks count the rows of the index, as they were when the
	// last cycle began or ended.
	Albums Counts
	Tracks Counts
	// Progress is nil when no cycle is running.
	Progress *Progress
	// Maintenance is whether the last cycle found the marker .maintenance.
	Maintenance bool
	// Problems are the problems and the warnings of the last finished
	// cycle, at most MaxProblems, ordered by path. They live in memory and
	// each cycle makes the list again.
	Problems []Problem
}

// remembered is what the scanner keeps, between two cycles, of an album
// folder with a certain receipt.
type remembered struct {
	receiptHash string
	relPath     string
	// failed: the tools could not read the album. It is not indexed again
	// until its receipt changes or the server starts again: ffprobe and
	// ffmpeg are not run every few minutes on an album that is broken
	// (§6.2).
	failed bool
	// problems are listed again at every cycle while the album stays as it
	// is: the problem of a failed album, and the warnings of an album that
	// was indexed, which is not looked at again while its receipt is the
	// same.
	problems []Problem
}

// Scanner keeps the index aligned with the library (§6). It compares what
// is on disk with what the index says and makes the second the first: a
// cycle can be repeated at any time and after any interruption, and it
// never deletes a row (I3).
//
// One cycle runs at a time. Run is its goroutine; Trigger and Status are
// safe for concurrent use.
type Scanner struct {
	ix       *Indexer
	workers  int
	interval time.Duration
	log      *slog.Logger

	// wake holds the one cycle that is asked for while another runs.
	wake chan Reason
	// stale wakes the job that computes fingerprints again (§6.6).
	stale chan struct{}

	// memory is, by album_id, what is remembered of the albums. Only the
	// goroutine of the cycles reads and writes it.
	memory map[string]remembered

	mu     sync.Mutex
	status Status
	// rechecks are the ids of the albums that Recheck asked to look at
	// again, until a cycle takes them. Guarded by mu.
	rechecks map[string]struct{}
}

// NewScanner returns the scanner of the library that ix indexes. workers is
// how many albums are indexed at once, and interval the time between the
// end of a cycle and the start of the next when nothing asks for one
// sooner.
func NewScanner(ix *Indexer, workers int, interval time.Duration, log *slog.Logger) *Scanner {
	return &Scanner{
		ix: ix, workers: workers, interval: interval, log: log,
		wake:     make(chan Reason, 1),
		stale:    make(chan struct{}, 1),
		memory:   map[string]remembered{},
		status:   Status{State: StateIdle},
		rechecks: map[string]struct{}{},
	}
}

// Recheck asks for a cycle that indexes the album with that id again, even
// if its folder and its receipt are those it was indexed from: a media
// endpoint found one of its files with another size or time than the index
// says (DESIGN.md §9.1). Indexing it again reads the size and the time of
// its files from the disk, and pairs the files the index knows by their
// SHA-256 without starting a process (§6.3). An album that the tools could
// not read with the same receipt is still skipped: a client does not make
// the scanner run ffprobe again (§6.2).
//
// albumID is the id of a row of albums, read from the index: the ids
// waiting for a cycle are at most as many as the albums. Recheck returns at
// once, and the cycles it asks for coalesce like those of Trigger.
func (s *Scanner) Recheck(albumID string) {
	s.mu.Lock()
	s.rechecks[albumID] = struct{}{}
	s.mu.Unlock()
	s.Trigger(ReasonFileReplaced)
}

// Trigger asks for a cycle and returns at once. Triggers coalesce: while a
// cycle runs at most one more waits, however many are asked for, and it
// starts when the running one ends (§6.1).
func (s *Scanner) Trigger(reason Reason) {
	select {
	case s.wake <- reason:
	default:
	}
}

// Status returns the state of the library. The caller owns the result.
func (s *Scanner) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	if st.LastScan != nil {
		scan := *st.LastScan
		st.LastScan = &scan
	}
	if st.Progress != nil {
		progress := *st.Progress
		st.Progress = &progress
	}
	st.Problems = slices.Clone(st.Problems)
	return st
}

// Run is the scanner: a cycle at once, then one after every interval and
// whenever Trigger asks, until ctx ends. It also runs the job that computes
// the fingerprints of another ffmpeg again (§6.6). It returns when both
// have stopped; a cycle that ctx interrupts leaves the index as its last
// commit made it, and the next start goes on from there (crash-only).
func (s *Scanner) Run(ctx context.Context) {
	var jobs sync.WaitGroup
	jobs.Go(func() { s.fingerprints(ctx) })
	defer jobs.Wait()

	reason := ReasonStartup
	for ctx.Err() == nil {
		s.cycle(ctx, reason)
		timer := time.NewTimer(s.interval)
		select {
		case <-ctx.Done():
		case reason = <-s.wake:
		case <-timer.C:
			reason = ReasonInterval
		}
		timer.Stop()
	}
}

// scanOutcome is how a cycle ended.
type scanOutcome struct {
	// state is what the scanner is left in: StateIdle, or the reason it
	// stopped before the end.
	state State
	// message is why the cycle did not go to its end; "" when it did.
	message  string
	problems []Problem
	// discovered, indexed, absent and moved count the candidates, the
	// albums written, the albums found gone and the tracks whose references
	// moved.
	discovered, indexed, absent, moved int
	optimized                          bool
}

// cycle is one cycle (§6.1):
//
//	P0  the markers: in maintenance, or without MusicLib's volume, nothing
//	    is touched;
//	P1  the album folders and their receipts;
//	P2  one folder for each album_id;
//	P3  the albums that are new or changed since they were indexed;
//	P4  those albums are indexed, a few at once, each in its transaction;
//	P5  the albums that are no longer there become unavailable;
//	P6  the references of the users follow the audio; the state.
func (s *Scanner) cycle(ctx context.Context, reason Reason) {
	s.count(ctx)
	if state := s.preconditions(); state != StateScanning {
		s.log.Info("scan skipped", "reason", string(reason), "state", string(state))
		return
	}
	started := s.ix.now()
	s.begin(started)
	out := s.scan(ctx)
	// The counters first: a status that says the cycle is over has those of
	// the index the cycle left.
	s.count(ctx)
	finished := s.ix.now()
	s.end(out, finished)
	s.log.Info("scan finished", "reason", string(reason), "ok", out.message == "", "state", string(out.state),
		"discovered", out.discovered, "indexed", out.indexed, "absent", out.absent, "references_moved", out.moved,
		"problems", len(out.problems), "optimized", out.optimized, "duration_ms", finished.Sub(started).Milliseconds())
	if out.message == "" {
		// A row that came back as it was may have a fingerprint of another
		// ffmpeg.
		select {
		case s.stale <- struct{}{}:
		default:
		}
	}
	// A cycle holds every receipt of the library at once, about 50 MB for
	// 20,000 albums, and frees them when it ends (that list is also the
	// peak of a cycle, 162 to 172 MB: the list DESIGN.md T30 says not to
	// keep, which no budget names). Nothing collects that garbage until the
	// collection the Go runtime forces two minutes later, so after each
	// cycle the server would be at rest over the 150 MB of the step S24 for
	// about two minutes (measured on 20,000 albums without this call: 167 MB
	// 30 s after a cycle and 76 MB four minutes after; 64 MB with it;
	// NOTES.md N-163). It costs one collection of a heap that is mostly
	// free.
	debug.FreeOSMemory()
}

// preconditions is P0: it reads the two markers and records what they say.
// It returns StateScanning when a cycle may go through the library, and
// otherwise the state the scanner rests in.
func (s *Scanner) preconditions() State {
	state := StateScanning
	maintenance, err := s.ix.root.Maintenance()
	mounted, mountedErr := s.ix.root.StorePresent()
	switch {
	case err != nil || mountedErr != nil:
		// The folder itself cannot be read.
		state = StateUnavailable
	case !mounted:
		state = StateUnavailable
	case maintenance:
		state = StateMaintenance
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Maintenance = maintenance
	if state != StateScanning {
		s.status.State = state
		s.status.Progress = nil
	}
	return state
}

// begin records that a cycle started.
func (s *Scanner) begin(started time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = StateScanning
	s.status.LastScan = &Scan{StartedAt: started}
	s.status.Progress = &Progress{}
}

// end records how the cycle ended, and its problems.
func (s *Scanner) end(out scanOutcome, finished time.Time) {
	slices.SortStableFunc(out.problems, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.RelPath, b.RelPath), cmp.Compare(a.Code, b.Code))
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = out.state
	s.status.LastScan.FinishedAt = &finished
	s.status.LastScan.OK = out.message == ""
	s.status.LastScan.Error = out.message
	s.status.Progress = nil
	s.status.Problems = out.problems[:min(len(out.problems), MaxProblems)]
}

// count reads the counters of the status from the index. A failure leaves
// them as they were: the status is served all the same.
func (s *Scanner) count(ctx context.Context) {
	var albums, tracks Counts
	err := s.ix.store.Read(ctx, func(q *store.Queries) error {
		a, err := q.CountAlbums(ctx)
		if err != nil {
			return err
		}
		for _, row := range a {
			if row.Available == 1 {
				albums.Available = row.Total
			} else {
				albums.Unavailable = row.Total
			}
		}
		t, err := q.CountTracks(ctx)
		if err != nil {
			return err
		}
		for _, row := range t {
			if row.Available == 1 {
				tracks.Available = row.Total
			} else {
				tracks.Unavailable = row.Total
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("counting the albums and the tracks failed", "code", "internal", "err", err.Error())
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Albums, s.status.Tracks = albums, tracks
}

// The messages of a cycle that did not go to its end.
const (
	messageInterrupted = "the scan was interrupted"
	messageInternal    = "an internal error: see the log of the server"
)

// scan is P1 to P6.
func (s *Scanner) scan(ctx context.Context) scanOutcome {
	out := scanOutcome{state: StateIdle}
	// fail ends the cycle for a failure that is not of the library: the end
	// of ctx, or the database.
	fail := func(what string, err error) scanOutcome {
		if ctx.Err() != nil {
			out.message = messageInterrupted
			return out
		}
		s.log.Error("the scan failed: "+what, "code", "internal", "err", err.Error())
		out.message = messageInternal
		return out
	}

	var albums []store.ListAlbumStatesRow
	err := s.ix.store.Read(ctx, func(q *store.Queries) (err error) {
		albums, err = q.ListAlbumStates(ctx)
		return err
	})
	if err != nil {
		return fail("reading the albums of the index", err)
	}
	known := make(map[string]store.ListAlbumStatesRow, len(albums))
	registered := make(map[string]string, len(albums))
	for _, a := range albums {
		known[a.ID], registered[a.ID] = a, a.RelPath
	}

	// P1 and P2.
	d, err := Discover(ctx, s.ix.root, registered)
	switch {
	case ctx.Err() != nil:
		return fail("", ctx.Err())
	case err != nil:
		// library/ itself cannot be listed: nothing is known of the
		// library, and the index is left as it is (§6.2).
		out.state = StateUnavailable
		out.message = "the library folder cannot be listed: " + reason(err)
		return out
	}
	out.discovered = len(d.Candidates)
	out.problems = slices.Clone(d.Problems)

	// P3.
	work, carried := s.plan(d, known)
	out.problems = append(out.problems, carried...)
	s.progress(Progress{Discovered: len(d.Candidates), Pending: len(work)})

	// P4.
	failures := 0
	for _, r := range s.indexAll(ctx, work) {
		id := r.candidate.Receipt.AlbumID
		problem := Problem{RelPath: r.candidate.RelPath, Code: Code(r.err)}
		if r.err != nil {
			problem.Message = r.err.Error()
		}
		switch {
		case !r.done || ctx.Err() != nil:
		case r.err == nil:
			out.indexed++
			delete(s.memory, id)
			if len(r.warnings) > 0 {
				s.memory[id] = remembered{receiptHash: r.candidate.ReceiptHash, relPath: r.candidate.RelPath, problems: r.warnings}
				out.problems = append(out.problems, r.warnings...)
			}
		case problem.Code == CodeAlbumChanged:
			// Not a problem: the next cycle indexes what is there then.
			delete(s.memory, id)
		case problem.Code == CodeProbeFailed || problem.Code == CodeFingerprintFailed:
			s.memory[id] = remembered{receiptHash: r.candidate.ReceiptHash, relPath: r.candidate.RelPath, failed: true, problems: []Problem{problem}}
			out.problems = append(out.problems, problem)
		case problem.Code != "":
			// A file that is missing or of another size costs nothing to
			// look for again: if it was a passing fault, the next cycle
			// finds the album whole (§6.3 step 2).
			delete(s.memory, id)
			out.problems = append(out.problems, problem)
		default:
			// The database refused the album, or failed. The other albums
			// go on, and this one is tried again at the next cycle.
			delete(s.memory, id)
			failures++
			s.log.Error("indexing an album failed", "code", "internal", "rel_path", r.candidate.RelPath, "err", r.err.Error())
		}
	}
	if ctx.Err() != nil {
		return fail("", ctx.Err())
	}

	// The markers again: a rebuild of MusicLib that began after P0 may have
	// emptied the library under the discovery, and what it did not find is
	// not gone.
	if state := s.preconditions(); state != StateScanning {
		out.state = state
		out.message = "the library became " + string(state) + " during the scan"
		return out
	}

	// P5.
	seen := make(map[string]bool, len(d.Candidates))
	for _, c := range d.Candidates {
		seen[c.Receipt.AlbumID] = true
	}
	for _, a := range albums {
		if a.Available != 1 || seen[a.ID] || d.Protects(a.RelPath) {
			continue
		}
		now := s.ix.now().UnixMilli()
		err := s.ix.store.WithWriteTx(ctx, func(q *store.Queries) error { return commitAbsent(ctx, q, a.ID, a.ArtistID, now) })
		if err != nil {
			return fail("marking the album "+a.ID+" unavailable", err)
		}
		out.absent++
	}

	// P6.
	if out.moved, err = s.followAudio(ctx); err != nil {
		return fail("moving the references of the users", err)
	}
	if out.indexed+out.absent >= optimizeAfter {
		if err := s.ix.store.Optimize(ctx); err != nil {
			return fail("optimizing the database", err)
		}
		out.optimized = true
	}
	if failures > 0 {
		out.message = fmt.Sprintf("%d albums could not be indexed for an internal error: see the log of the server", failures)
	}
	return out
}

// plan is P3: the candidates that are not in the index as they are on disk
// are the work of the cycle. An album whose folder and receipt are those it
// was indexed from is skipped, unless Recheck asked for it since the last
// cycle; one that the tools could not read with this very receipt is
// skipped all the same. carried are the problems and the warnings
// remembered of the albums that are skipped. It also forgets the albums
// that are no longer what was remembered of them, and the ids Recheck gave
// that are not among the candidates.
func (s *Scanner) plan(d Discovery, known map[string]store.ListAlbumStatesRow) (work []Candidate, carried []Problem) {
	s.mu.Lock()
	rechecks := s.rechecks
	s.rechecks = map[string]struct{}{}
	s.mu.Unlock()
	memory := map[string]remembered{}
	for _, c := range d.Candidates {
		id := c.Receipt.AlbumID
		m, ok := s.memory[id]
		same := ok && m.receiptHash == c.ReceiptHash && m.relPath == c.RelPath
		a, indexed := known[id]
		_, recheck := rechecks[id]
		current := indexed && a.Available == 1 && a.ReceiptHash == c.ReceiptHash && a.RelPath == c.RelPath && !recheck
		switch {
		case same && (m.failed || current):
			memory[id] = m
			carried = append(carried, m.problems...)
		case current:
		default:
			work = append(work, c)
		}
	}
	// An album below an artist folder that could not be listed was not
	// looked for: what is remembered of it still holds.
	for id, m := range s.memory {
		if _, kept := memory[id]; !kept && d.Protects(m.relPath) {
			memory[id] = m
		}
	}
	s.memory = memory
	return work, carried
}

// progress records how far the cycle has come.
func (s *Scanner) progress(p Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Progress = &p
}

// indexing is what indexing one album of the work list gave.
type indexing struct {
	candidate Candidate
	// done is false for an album the cycle did not get to, because ctx
	// ended.
	done     bool
	warnings []Problem
	err      error
}

// indexAll is P4: it indexes the albums of the work list, at most
// s.workers at once, each in its own write transaction. An album that fails
// does not stop the others; the end of ctx does.
func (s *Scanner) indexAll(ctx context.Context, work []Candidate) []indexing {
	results := make([]indexing, len(work))
	next := make(chan int)
	var workers sync.WaitGroup
	for range min(s.workers, len(work)) {
		workers.Go(func() {
			for i := range next {
				r := &results[i]
				r.candidate = work[i]
				r.warnings, r.err = s.ix.IndexAlbum(ctx, work[i])
				r.done = true
				s.mu.Lock()
				s.status.Progress.Pending--
				if r.err == nil {
					s.status.Progress.Indexed++
				}
				s.mu.Unlock()
			}
		})
	}
feed:
	for i := range work {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	workers.Wait()
	return results
}

// followAudio is the rule of P6: the playlist items and the favorites of a
// track that is not available move to an available track with the same
// audio, and that track keeps the moment the audio was first seen
// (commitReferences). It runs at the end of every cycle that went through
// the library, whether the cycle changed anything or not, so that a stop
// between the commit of an album and this step is repaired by the next
// cycle. When there is nothing to follow, nothing is written. moved is how
// many tracks lost their references.
func (s *Scanner) followAudio(ctx context.Context) (moved int, err error) {
	var rows []store.ListAudioTwinsRow
	err = s.ix.store.Read(ctx, func(q *store.Queries) (err error) {
		rows, err = q.ListAudioTwins(ctx)
		return err
	})
	if err != nil || len(followed(rows)) == 0 {
		return 0, err
	}
	now := s.ix.now().UnixMilli()
	err = s.ix.store.WithWriteTx(ctx, func(q *store.Queries) error { return commitReferences(ctx, q, now, &moved) })
	return moved, err
}
