package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// What MusicLib 1.2.0 added, checked for step S6m (the Errata of DESIGN.md):
// a track added to an album (H12) and tracks moved to another album (H10,
// H11). Vibrance follows a moved track by its fingerprint, so the
// fingerprint must survive the move, and the receipts of both albums must
// say where the file is.

// addTrack is POST /api/albums/{id}/tracks?name=<file name>: the body is the
// audio file.
func (c *client) addTrack(ctx context.Context, a album, name string, data []byte) error {
	return c.call(ctx, http.MethodPost, "/api/albums/"+a.ID+"/tracks?name="+url.QueryEscape(name),
		map[string]string{"If-Match": a.ETag}, rawBody{data: data, contentType: "application/octet-stream"}, nil)
}

// moveAnswer is the answer of POST /api/albums/{id}/move-tracks: both
// albums at their new revision.
type moveAnswer struct {
	From album `json:"from"`
	To   album `json:"to"`
}

// moveTracks moves the tracks ids of album from to album to; the If-Match
// is the source's.
func (c *client) moveTracks(ctx context.Context, from album, ids []string, to string) (moveAnswer, error) {
	var ans moveAnswer
	err := c.call(ctx, http.MethodPost, "/api/albums/"+from.ID+"/move-tracks", map[string]string{"If-Match": from.ETag},
		map[string]any{"tracks": ids, "to": to}, &ans)
	return ans, err
}

// state waits until MusicLib has published album id with a build other than
// prevBuild, and reads it from disk.
func (c *client) state(ctx context.Context, label, id, prevBuild string) (albumState, error) {
	p, err := c.waitPublished(ctx, id, prevBuild)
	if err != nil {
		return albumState{}, err
	}
	return capture(ctx, label, p)
}

// extraTrack writes, with the pinned ffmpeg, the FLAC that H12 adds to the
// album of spec: a sine of a frequency that no input has, tagged for the
// place after the album's last track. It returns the file's name and bytes.
func extraTrack(ctx context.Context, spec albumSpec, freq int) (string, []byte, error) {
	t := trackSpec{Disc: 1, No: len(spec.Tracks) + 1, Title: "Added Later", Freq: freq}
	spec.Format, spec.RG = "flac", false
	spec.Tracks = append(slices.Clone(spec.Tracks), t)
	name := fmt.Sprintf("%02d %s.flac", t.No, t.Title)
	out := filepath.Join(workRoot, "added", spec.Key, name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", nil, err
	}
	if err := ffmpegGenerate(ctx, append(sineArgs(t, "flac"), metadataArgs(spec, t)...), out); err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(out)
	return name, data, err
}

// diskState is the album_revision that the two albums of a move have on
// disk at one instant; 0 means that no folder carries that album_id.
type diskState struct {
	At       time.Duration // since the poller started
	From, To int64
}

// movePoll is what a poller of the library saw during a move.
type movePoll struct {
	// States is every change of the pair of revisions, in order.
	States []diskState
	Passes int
	// Partial is every album folder seen incomplete (watchResult.Partial).
	Partial []string
}

// pollMove reads the library at root in a loop until ctx is done, and then
// once more, so that its last state is the one after the move.
func pollMove(ctx context.Context, root, fromID, toID string) (movePoll, error) {
	var p movePoll
	start := time.Now()
	for last := false; !last; {
		last = ctx.Err() != nil
		at := time.Since(start)
		seen, partial, err := readPass(root)
		if err != nil {
			return p, err
		}
		p.Passes++
		p.Partial = append(p.Partial, partial...)
		s := diskState{At: at, From: highest(seen[fromID]), To: highest(seen[toID])}
		if n := len(p.States); n == 0 || p.States[n-1].From != s.From || p.States[n-1].To != s.To {
			p.States = append(p.States, s)
		}
	}
	return p, nil
}

func highest(revs []int64) int64 {
	if len(revs) == 0 {
		return 0
	}
	return slices.Max(revs)
}

// moveOrder says which of the two albums of a move was published first,
// from the changes a poller saw. src and dst are the revisions the albums
// had on disk before the move; trashed says that the move left the source
// without tracks, so its publication is the removal of its folder.
func moveOrder(states []diskState, src, dst int64, trashed bool) string {
	i := slices.IndexFunc(states, func(s diskState) bool {
		if trashed {
			return s.From == 0
		}
		return s.From > src
	})
	j := slices.IndexFunc(states, func(s diskState) bool { return s.To > dst })
	switch {
	case i < 0 || j < 0:
		return "not observed: the poller did not see both albums change"
	case i < j:
		return fmt.Sprintf("the source first: for %s the moved tracks were in neither album on disk", (states[j].At - states[i].At).Round(time.Millisecond))
	case j < i:
		return fmt.Sprintf("the destination first: for %s the moved tracks were in both albums on disk", (states[i].At - states[j].At).Round(time.Millisecond))
	}
	return "both between two passes: the poller never saw one album updated and the other not"
}

func statesLine(states []diskState) string {
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = fmt.Sprintf("+%s %d/%d", s.At.Round(time.Millisecond), s.From, s.To)
	}
	return strings.Join(parts, ", ")
}

// move is one call of POST /api/albums/{id}/move-tracks, with the two
// albums as read from disk before and after it.
type move struct {
	Label string
	// Tracks are the moved tracks, as they were in the source.
	Tracks   []trackState
	Src, Dst albumState
	// Trashed says that the move left the source without tracks: MusicLib
	// put it in the trash, and SrcAfter is empty.
	Trashed            bool
	SrcAfter, DstAfter albumState
	// Left is, for a trashed source, one line for everything of it that is
	// still in the library; ArtistDir says what became of its artist folder.
	Left []string
	// Folders is how many folders of the library still carry the album_id
	// of a trashed source.
	Folders   int
	ArtistDir string
	Poll      movePoll
}

// doMove moves the tracks ids of the album of src to the album of dst while
// a poller reads the library, waits for both publications and reads the two
// albums again.
func doMove(ctx context.Context, c *client, label string, src, dst albumState, ids []string) (move, error) {
	m := move{Label: label, Src: src, Dst: dst}
	for _, id := range ids {
		i := slices.IndexFunc(src.Tracks, func(t trackState) bool { return t.ID == id })
		if i < 0 {
			return move{}, fmt.Errorf("%s: the source has no track %s", label, id)
		}
		m.Tracks = append(m.Tracks, src.Tracks[i])
	}
	pctx, stop := context.WithCancel(ctx)
	type polled struct {
		poll movePoll
		err  error
	}
	done := make(chan polled, 1)
	go func() {
		p, err := pollMove(pctx, libraryRoot, src.API.ID, dst.API.ID)
		done <- polled{p, err}
	}()
	var from, to published
	err := func() error {
		ans, err := c.moveTracks(ctx, src.API, ids, dst.API.ID)
		if err != nil {
			return err
		}
		if m.Trashed = ans.From.Trashed; m.Trashed {
			if len(ans.From.Tracks) != 0 {
				m.Left = append(m.Left, fmt.Sprintf("the API lists %d tracks in the trashed source", len(ans.From.Tracks)))
			}
			err = c.waitGone(ctx, src.API.ID)
		} else {
			from, err = c.waitPublished(ctx, src.API.ID, src.Dir.Receipt.BuildID)
		}
		if err != nil {
			return err
		}
		to, err = c.waitPublished(ctx, dst.API.ID, dst.Dir.Receipt.BuildID)
		return err
	}()
	stop()
	p := <-done
	m.Poll = p.poll
	if err := errors.Join(err, p.err); err != nil {
		return move{}, fmt.Errorf("%s: %w", label, err)
	}
	if m.Trashed {
		if err := m.readTrashedSource(); err != nil {
			return move{}, err
		}
	} else if m.SrcAfter, err = capture(ctx, label, from); err != nil {
		return move{}, err
	}
	if m.DstAfter, err = capture(ctx, label, to); err != nil {
		return move{}, err
	}
	return m, nil
}

// readTrashedSource looks in the library for what is left of the source of
// a move that took all its tracks: nothing should be.
func (m *move) readTrashedSource() error {
	dirs, err := findAlbum(m.Src.API.ID)
	if err != nil {
		return err
	}
	m.Folders = len(dirs)
	for _, d := range dirs {
		m.Left = append(m.Left, "a folder still carries its album_id: "+d.Rel)
	}
	old := filepath.Join(libraryRoot, filepath.FromSlash(m.Src.Dir.Rel))
	if _, err := os.Lstat(old); !errors.Is(err, fs.ErrNotExist) {
		m.Left = append(m.Left, fmt.Sprintf("its folder %s is still there (Lstat: %v)", m.Src.Dir.Rel, err))
	}
	_, err = os.Lstat(filepath.Dir(old))
	m.ArtistDir = "the artist folder `" + path.Dir(m.Src.Dir.Rel) + "` " + presence(err)
	return nil
}

func (m move) ids() []string {
	ids := make([]string, len(m.Tracks))
	for i, t := range m.Tracks {
		ids[i] = t.ID
	}
	slices.Sort(ids)
	return ids
}

// fingerprintProblems is H10 for one move: one line for every moved track
// that the destination does not have with the fingerprint it had in the
// source, and for every other track of the two albums whose fingerprint
// changed. nil when the move kept every fingerprint.
func (m move) fingerprintProblems() []string {
	var out []string
	for _, t := range m.Tracks {
		after, ok := trackByID(m.DstAfter, t.ID)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("moved track %s is not in the destination", t.ID))
		case after.FP != t.FP:
			out = append(out, fmt.Sprintf("moved track %s: %s, was %s", t.ID, after.FP, t.FP))
		}
	}
	changed, _, _ := trackChanges(m.Dst, m.DstAfter)
	out = append(out, changed...)
	if !m.Trashed {
		changed, _, _ = trackChanges(m.Src, m.SrcAfter)
		out = append(out, changed...)
	}
	return out
}

// receiptProblems is H11 for one move, as a reader of the library sees it:
// the destination's receipt lists a file for every moved track and for
// every track it had; the source's lists its other tracks and no file with
// the audio of a moved one, or, when the move took every track, nothing of
// the source is left in the library. Both receipts match their folders. It
// returns one line per violation.
func (m move) receiptProblems() []string {
	ids := m.ids()
	out := stateProblems("the destination", m.DstAfter)
	if _, added, removed := trackChanges(m.Dst, m.DstAfter); !slices.Equal(added, ids) || len(removed) != 0 {
		out = append(out, fmt.Sprintf("the destination gained the tracks %v and lost %v, the move was of %v", added, removed, ids))
	}
	if m.Trashed {
		return append(out, m.Left...)
	}
	out = append(out, stateProblems("the source", m.SrcAfter)...)
	if _, added, removed := trackChanges(m.Src, m.SrcAfter); len(added) != 0 || !slices.Equal(removed, ids) {
		out = append(out, fmt.Sprintf("the source gained the tracks %v and lost %v, the move was of %v", added, removed, ids))
	}
	for _, t := range m.Tracks {
		for _, left := range m.SrcAfter.Tracks {
			if left.FP == t.FP {
				out = append(out, fmt.Sprintf("the source still lists %s, with the audio of the moved track %s", left.Rel, t.ID))
			}
		}
	}
	return out
}

// stateProblems checks one album read from disk: its receipt against its
// folder, and one audio file listed for each track of MusicLib's API
// (capture has paired every track with a file).
func stateProblems(who string, s albumState) []string {
	var out []string
	for _, p := range s.Problems {
		out = append(out, who+": "+p)
	}
	if n := audioFiles(s.Dir.Receipt); n != len(s.Tracks) {
		out = append(out, fmt.Sprintf("%s: the receipt lists %d audio files, the album has %d tracks", who, n, len(s.Tracks)))
	}
	return out
}

func audioFiles(r receipt) int {
	n := 0
	for _, f := range r.Files {
		if isAudio(f.RelativePath) {
			n++
		}
	}
	return n
}

func trackByID(s albumState, id string) (trackState, bool) {
	i := slices.IndexFunc(s.Tracks, func(t trackState) bool { return t.ID == id })
	if i < 0 {
		return trackState{}, false
	}
	return s.Tracks[i], true
}

// The moves of H10, one per codec: the track at disc 1, number 2 of the
// codec's album goes to another album. The FLAC track goes to an album
// without a cover and loses its embedded picture; the others go to album A
// and gain its cover.
var codecMoves = []struct{ Codec, From, To string }{
	{"FLAC", "A", "E"}, {"MP3", "B", "A"}, {"AAC", "C", "A"}, {"ALAC", "D", "A"},
}

// runMoves is H10, H11 and H12, and the observation of the order in which
// MusicLib publishes the two albums of a move.
func runMoves(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	// The albums as they are now, by their letter.
	cur := map[string]albumState{}
	for _, key := range []string{"A", "B", "C", "D", "E"} {
		a, err := c.albumByTitle(ctx, all, specByKey(key).Title)
		if err != nil {
			return err
		}
		if cur[key], err = c.state(ctx, "before", a.ID, ""); err != nil {
			return err
		}
	}

	// H12.
	var h12 fragment
	h12.b.WriteString("## H12: adding a track keeps the fingerprints of the album's tracks\n\n")
	h12.para("Claim (Errata of `DESIGN.md`, step S6m): after `POST /api/albums/<id>/tracks` MusicLib writes the album again with one more track, and the fingerprint of every track the album already had is identical. To each of the albums A–D (FLAC, MP3, AAC, ALAC) a new FLAC track is added: a sine of another frequency, written by the pinned ffmpeg. Tracks are followed by MusicLib's track id.")
	addOK := true
	for i, ca := range codecAlbums {
		before := cur[ca.Key]
		name, data, err := extraTrack(ctx, specByKey(ca.Key), 740+40*i)
		if err != nil {
			return err
		}
		if err := c.addTrack(ctx, before.API, name, data); err != nil {
			return err
		}
		after, err := c.state(ctx, "track added", before.API.ID, before.Dir.Receipt.BuildID)
		if err != nil {
			return err
		}
		cur[ca.Key] = after
		changed, added, removed := trackChanges(before, after)
		problems := append(changed, stateProblems("the album", after)...)
		if len(added) != 1 || len(removed) != 0 {
			problems = append(problems, fmt.Sprintf("%d tracks are new and %d are gone, want 1 and 0", len(added), len(removed)))
		}
		h12.heading("%s: album %s (%s)", ca.Codec, ca.Key, after.Dir.Rel)
		h12.para("`POST /api/albums/%s/tracks?name=%s` (%d bytes); `album_revision` %d → %d.",
			before.API.ID, url.QueryEscape(name), len(data), before.Dir.Receipt.AlbumRevision, after.Dir.Receipt.AlbumRevision)
		var rows [][]string
		for _, t := range after.Tracks {
			old, ok := trackByID(before, t.ID)
			was, res := "-", "the added track"
			if ok {
				was, res = fileCell(old), "identical"
				if old.FP != t.FP {
					res = "DIFFERENT, was " + old.FP
				}
			}
			rows = append(rows, []string{t.ID, was, fileCell(t), t.FP, res})
		}
		h12.table([]string{"MusicLib track id", "file before (sha256 prefix)", "file after (sha256 prefix)", "fingerprint after", "vs. before"}, rows)
		if len(problems) > 0 {
			addOK = false
			h12.para("Problems: %s.", strings.Join(problems, "; "))
		}
		h12.commands("Every fingerprint command of album "+ca.Key+", before and after", append(slices.Clone(before.Tracks), after.Tracks...))
	}
	if err := verdict("H12 track added", addOK,
		"after a track is added to a FLAC, an MP3, an AAC and an ALAC album, every track the album had keeps its fingerprint, and the receipt lists one more audio file"); err != nil {
		return err
	}

	// H10 and H11: one track per codec moved to another album.
	var moves []move
	for _, cm := range codecMoves {
		src := cur[cm.From]
		i := slices.IndexFunc(src.Tracks, func(t trackState) bool { return t.Disc == 1 && t.No == 2 })
		if i < 0 {
			return fmt.Errorf("album %s has no track 2 on disc 1", cm.From)
		}
		m, err := doMove(ctx, c, fmt.Sprintf("%s: track 1-2 of album %s to album %s", cm.Codec, cm.From, cm.To), src, cur[cm.To], []string{src.Tracks[i].ID})
		if err != nil {
			return err
		}
		if m.Trashed {
			return fmt.Errorf("%s: the source went to the trash, it had other tracks", m.Label)
		}
		cur[cm.From], cur[cm.To] = m.SrcAfter, m.DstAfter
		moves = append(moves, m)
	}
	// The title and the artist of every moved track change in its new album.
	edited := map[string]albumState{} // by the letter of the destination
	for _, key := range []string{"E", "A"} {
		s := cur[key]
		u := s.API.update()
		for n, cm := range codecMoves {
			if cm.To != key {
				continue
			}
			i := slices.IndexFunc(u.Tracks, func(t trackUpdate) bool { return t.ID == moves[n].Tracks[0].ID })
			if i < 0 {
				return fmt.Errorf("album %s: MusicLib's API does not list the moved %s track", key, cm.Codec)
			}
			artist := "Mover of " + cm.Codec
			u.Tracks[i].Title, u.Tracks[i].Artist = "Moved "+cm.Codec, &artist
		}
		if err := c.putAlbum(ctx, s.API, u); err != nil {
			return err
		}
		after, err := c.state(ctx, "title and artist changed", s.API.ID, s.Dir.Receipt.BuildID)
		if err != nil {
			return err
		}
		edited[key], cur[key] = after, after
	}
	// Every track left in album B, the MP3 one and the FLAC of H12, moves to
	// album E: B is left without tracks.
	whole, err := doMove(ctx, c, "all the tracks of album B to album E", cur["B"], cur["E"], idsOf(cur["B"]))
	if err != nil {
		return err
	}
	cur["E"] = whole.DstAfter

	var h10, h11, order fragment
	h10.b.WriteString("## H10: the fingerprint survives a move to another album\n\n")
	h10.para("Claim (Errata of `DESIGN.md`, step S6m): the fingerprint of a track is identical before and after `POST /api/albums/<id>/move-tracks` takes it to another album, also when its title and its artist are changed there afterwards, for FLAC, MP3, AAC and ALAC. The track keeps its MusicLib track id; its file is written again in the folder of the other album, with that album's tags and cover. The FLAC track goes to album E, which has no cover; the MP3, AAC and ALAC tracks go to album A, a FLAC album with a cover. Then the titles and the artists of the moved tracks are changed (`PUT /api/albums/<id>`), and at the end every track left in album B goes to album E.")
	for n, cm := range codecMoves {
		m := moves[n]
		t := m.Tracks[0]
		problems := m.fingerprintProblems()
		states := []albumState{m.Src, m.DstAfter, edited[cm.To]}
		runs := []trackState{t}
		if after, ok := trackByID(m.DstAfter, t.ID); ok {
			runs = append(runs, after)
		}
		last, ok := trackByID(edited[cm.To], t.ID)
		switch {
		case !ok:
			problems = append(problems, "the track is gone after the change of title and artist")
		case last.FP != t.FP:
			problems = append(problems, fmt.Sprintf("after the change of title and artist: %s, was %s", last.FP, t.FP))
		}
		if ok {
			runs = append(runs, last)
		}
		changed, _, _ := trackChanges(m.DstAfter, edited[cm.To])
		// DstAfter is the destination right after this move; later moves
		// into the same album only add tracks.
		problems = append(problems, changed...)
		h10.heading("%s: `%s` to `%s`", cm.Codec, m.Src.Dir.Rel, m.DstAfter.Dir.Rel)
		h10.para("`POST /api/albums/%s/move-tracks` with `{\"tracks\":[\"%s\"],\"to\":\"%s\"}`.", m.Src.API.ID, t.ID, m.Dst.API.ID)
		h10.trackRows([]string{"before the move", "after the move", "after the change of title and artist"}, states, t)
		if cm.Codec == "MP3" {
			problems = append(problems, whole.fingerprintProblems()...)
			h10.para("The move of every track left in album B (`%s`) to album E (`%s`), `{\"tracks\":[\"%s\"],\"to\":\"%s\"}`: the same MP3 album's other track, and the FLAC track that H12 added.",
				whole.Src.Dir.Rel, whole.DstAfter.Dir.Rel, strings.Join(idsOf(whole.Src), `","`), whole.Dst.API.ID)
			for _, wt := range whole.Tracks {
				h10.trackRows([]string{"before the move", "after the move"}, []albumState{whole.Src, whole.DstAfter}, wt)
				runs = append(runs, wt)
				if after, ok := trackByID(whole.DstAfter, wt.ID); ok {
					runs = append(runs, after)
				}
			}
		}
		res := "Every fingerprint is identical; the other tracks of the albums kept theirs too."
		if len(problems) > 0 {
			res = "Problems: " + strings.Join(problems, "; ") + "."
		}
		h10.para("%s", res)
		h10.commands("Every fingerprint command of these rows", runs)
		if err := verdict(fmt.Sprintf("H10.%d moved track, %s", n+1, cm.Codec), len(problems) == 0,
			fmt.Sprintf("the fingerprint of the moved %s track is identical before its move to another album, after it, and after its title and artist are changed there", cm.Codec)); err != nil {
			return err
		}
	}

	h11.b.WriteString("## H11: what a move leaves in the library\n\n")
	h11.para("Claim (Errata of `DESIGN.md`, step S6m): after a move of some tracks of an album, the receipt of the album they left no longer lists the moved file and the receipt of the other album lists it; after a move of all its tracks, the folder of the album disappears from `library/`. A file is recognized as Vibrance will, by its fingerprint: no audio file of the source may have the fingerprint of the moved track. Every receipt is also compared with its folder (H5), and must list one audio file for each track of MusicLib's API.")
	h11.heading("Some tracks of an album")
	partOK := true
	var rows [][]string
	for _, m := range moves {
		res := "as claimed"
		if p := m.receiptProblems(); len(p) > 0 {
			partOK, res = false, strings.Join(p, "; ")
		}
		t := m.Tracks[0]
		after, _ := trackByID(m.DstAfter, t.ID)
		rows = append(rows, []string{m.Label,
			fmt.Sprintf("`%s`: %d → %d audio files; `%s` listed before", m.Src.Dir.Rel, audioFiles(m.Src.Dir.Receipt), audioFiles(m.SrcAfter.Dir.Receipt), t.Rel),
			audioList(m.SrcAfter),
			fmt.Sprintf("`%s`: %d → %d audio files; the moved track is `%s`", m.DstAfter.Dir.Rel, audioFiles(m.Dst.Dir.Receipt), audioFiles(m.DstAfter.Dir.Receipt), after.Rel),
			res})
	}
	h11.table([]string{"move", "source receipt", "audio files the source lists after (fingerprint prefix)", "destination receipt", "result"}, rows)
	for _, m := range moves {
		h11.b.WriteString("<details><summary>" + m.Label + ": the two receipts right after the move</summary>\n\n")
		for _, s := range []albumState{m.SrcAfter, m.DstAfter} {
			h11.receipt(s)
		}
		h11.b.WriteString("</details>\n\n")
	}
	h11.heading("All the tracks of an album")
	wholeProblems := whole.receiptProblems()
	if !whole.Trashed {
		wholeProblems = append(wholeProblems, "MusicLib did not put the source in the trash")
	}
	h11.para("`POST /api/albums/%s/move-tracks` with the %d tracks left in album B (`%s`), to album E. MusicLib's answer has the source with `\"trashed\": %v`. After both publications: %d folders of `library/` carry the `album_id` of B; %s; the destination `%s` lists %d → %d audio files.",
		whole.Src.API.ID, len(whole.Tracks), whole.Src.Dir.Rel, whole.Trashed, whole.Folders, whole.ArtistDir,
		whole.DstAfter.Dir.Rel, audioFiles(whole.Dst.Dir.Receipt), audioFiles(whole.DstAfter.Dir.Receipt))
	entries, err := os.ReadDir(libraryRoot)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	h11.para("`library/` now %s.", listing(names, nil))
	res := "As claimed."
	if len(wholeProblems) > 0 {
		res = "Problems: " + strings.Join(wholeProblems, "; ") + "."
	}
	h11.para("%s", res)
	h11.b.WriteString("<details><summary>The receipt of the destination after the move</summary>\n\n")
	h11.receipt(whole.DstAfter)
	h11.b.WriteString("</details>\n\n")
	if err := verdict("H11.1 some tracks moved", partOK,
		"after a move of one track, for each codec, the receipt of the source lists no file with that audio and the receipt of the destination lists it; both receipts match their folders"); err != nil {
		return err
	}
	if err := verdict("H11.2 all tracks moved", len(wholeProblems) == 0,
		"after a move of all its tracks the album is in MusicLib's trash and its folder is gone from library/; the destination lists the files"); err != nil {
		return err
	}

	order.heading("Observation: the order in which MusicLib publishes the two albums of a move")
	order.para("Not a claim, so no verdict. MusicLib changes both albums in one transaction and queues one render for each (its `internal/catalog/move.go`); a reader of the library can see the moment between the two publications. During every move above a poller read all the receipts of `library/` in a loop and recorded each change of the pair of `album_revision` values on disk (source/destination, 0: no folder with that `album_id`), with the time since the poller started. How often a reader sees the intermediate state depends on timing; that it exists does not.")
	rows = nil
	for _, m := range append(slices.Clone(moves), whole) {
		partial := "none"
		if len(m.Poll.Partial) > 0 {
			partial = strings.Join(m.Poll.Partial, "; ")
		}
		rows = append(rows, []string{m.Label, fmt.Sprint(m.Poll.Passes), statesLine(m.Poll.States),
			moveOrder(m.Poll.States, m.Src.Dir.Receipt.AlbumRevision, m.Dst.Dir.Receipt.AlbumRevision, m.Trashed), partial})
	}
	order.table([]string{"move", "passes", "revisions on disk (source/destination)", "published first", "incomplete folders seen"}, rows)

	for name, f := range map[string]*fragment{"H10": &h10, "H11a": &h11, "H11b": &order, "H12": &h12} {
		if err := f.save(name); err != nil {
			return err
		}
	}
	return nil
}

func idsOf(s albumState) []string {
	ids := make([]string, len(s.Tracks))
	for i, t := range s.Tracks {
		ids[i] = t.ID
	}
	return ids
}

// fileCell is a track's file with the first digits of its SHA-256.
func fileCell(t trackState) string {
	return fmt.Sprintf("`%s` (%s)", t.Rel, t.SHA256[:12])
}

// audioList is every track file of s with the first digits of its
// fingerprint.
func audioList(s albumState) string {
	parts := make([]string, len(s.Tracks))
	for i, t := range s.Tracks {
		parts[i] = fmt.Sprintf("`%s` (%s)", t.Rel, t.FP[:12])
	}
	return strings.Join(parts, ", ")
}

// trackRows writes a table that follows track t through states, one row
// per state that has it, with labels as the names of the states.
func (f *fragment) trackRows(labels []string, states []albumState, t trackState) {
	var rows [][]string
	for i, s := range states {
		st, ok := trackByID(s, t.ID)
		if !ok {
			rows = append(rows, []string{labels[i], "`" + s.Dir.Rel + "`", "the track is not there", "-", "-", "-", "-"})
			continue
		}
		res := "-"
		if i > 0 {
			res = "identical"
			if st.FP != t.FP {
				res = "DIFFERENT"
			}
		}
		rows = append(rows, []string{labels[i], "`" + s.Dir.Rel + "`", fileCell(st), apiTrackLine(s.API, t.ID), st.Streams, st.FP, res})
	}
	f.table([]string{"state", "album folder", "file (sha256 prefix)", "title and artist (MusicLib's API)", "streams (ffprobe)", "fingerprint", "vs. before the move"}, rows)
}

// apiTrackLine is the title and the artist of track id as MusicLib's API
// gives them; a track without an artist of its own shows the album's.
func apiTrackLine(a album, id string) string {
	i := slices.IndexFunc(a.Tracks, func(t track) bool { return t.ID == id })
	if i < 0 {
		return "-"
	}
	artist := a.ArtistName + " (the album's)"
	if t := a.Tracks[i]; t.Artist != nil {
		artist = *t.Artist
	}
	return a.Tracks[i].Title + ", " + artist
}

// commands writes, folded, the fingerprint command of every track of
// tracks with its output.
func (f *fragment) commands(summary string, tracks []trackState) {
	f.b.WriteString("<details><summary>" + summary + "</summary>\n\n")
	for _, t := range tracks {
		f.command(t.FPRun.Cmdline, string(t.FPRun.Stdout))
	}
	f.b.WriteString("</details>\n\n")
}

// receipt writes the receipt of s as it was on disk when s was read:
// parseReceipt accepted it only because its bytes were exactly this
// canonical form.
func (f *fragment) receipt(s albumState) {
	f.command("cat "+shellQuote(path.Join(libraryRoot, s.Dir.Rel, receiptName)), string(canonicalReceipt(s.Dir.Receipt)))
}
