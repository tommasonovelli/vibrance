package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// trackState is one track of an album in one state, read from disk.
type trackState struct {
	ID       string
	Disc, No int
	Title    string
	Rel      string // relative to the album folder
	SHA256   string // from the receipt
	FP       string
	FPRun    toolRun
	Streams  string // the streams, as "index:type/codec[attached_pic]"
	CoverOK  string // how the video streams compare with the cover file
	coverBad bool
}

// albumState is an album after one step of the edits.
type albumState struct {
	Label    string
	API      album
	Dir      albumDir
	Problems []string // the receipt against the folder (checkAlbumFiles)
	Tracks   []trackState
	// Watch is what a library watcher saw during the step; OpenFile is the
	// result of reading, after the step, a track file opened before it.
	Watch    watchResult
	OpenFile string
	openBad  bool
}

// capture reads the published album p from disk: the receipt against the
// folder, and for every track of the API its file (paired by disc and
// number, DESIGN.md §4.1), its fingerprint and its streams (H4).
func capture(ctx context.Context, label string, p published) (albumState, error) {
	dir := filepath.Join(libraryRoot, filepath.FromSlash(p.Dir.Rel))
	problems, err := checkAlbumFiles(dir, p.Dir.Receipt)
	if err != nil {
		return albumState{}, err
	}
	s := albumState{Label: label, API: p.Album, Dir: p.Dir, Problems: problems}
	cover := ""
	for _, f := range p.Dir.Receipt.Files {
		if f.RelativePath == "cover.jpg" || f.RelativePath == "cover.png" {
			cover = f.SHA256
		}
	}
	for _, t := range p.Album.Tracks {
		ts := trackState{ID: t.ID, Disc: t.Disc, No: t.No, Title: t.Title}
		for _, f := range p.Dir.Receipt.Files {
			if d, n, ok := trackSlot(f.RelativePath); ok && isAudio(f.RelativePath) && d == t.Disc && n == t.No {
				ts.Rel, ts.SHA256 = f.RelativePath, f.SHA256
			}
		}
		if ts.Rel == "" {
			return albumState{}, fmt.Errorf("%s: no file for track %d-%d %q", p.Dir.Rel, t.Disc, t.No, t.Title)
		}
		file := filepath.Join(dir, filepath.FromSlash(ts.Rel))
		if ts.FP, ts.FPRun, err = fingerprint(ctx, file); err != nil {
			return albumState{}, err
		}
		if err := checkStreams(ctx, file, cover, &ts); err != nil {
			return albumState{}, err
		}
		s.Tracks = append(s.Tracks, ts)
	}
	return s, nil
}

// checkStreams lists the streams of file (H4) and compares every video
// stream with the album's cover file (coverSHA, "" when the album has
// none): each must be an attached picture whose bytes are the cover's.
func checkStreams(ctx context.Context, file, coverSHA string, ts *trackState) error {
	p, _, err := probe(ctx, file)
	if err != nil {
		return err
	}
	var desc []string
	video := 0
	for _, st := range p.Streams {
		d := fmt.Sprintf("%d:%s/%s", st.Index, st.CodecType, st.CodecName)
		if st.Disposition["attached_pic"] == 1 {
			d += "[attached_pic]"
		}
		desc = append(desc, d)
		if st.CodecType != "video" {
			continue
		}
		if st.Disposition["attached_pic"] != 1 {
			ts.CoverOK, ts.coverBad = fmt.Sprintf("stream %d is a video that is not an attached picture", st.Index), true
			continue
		}
		demux, err := demuxer(file)
		if err != nil {
			return err
		}
		// The picture's packet, as is, on stdout. Not the hash muxer: it
		// refuses a video stream whose dimensions the probe did not read
		// ("dimensions not set", seen with MP3); image2pipe does not need them.
		args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
			"-protocol_whitelist", "fd", "-fd", "3", "-f", demux, "-i", "fd:",
			"-map", fmt.Sprintf("0:%d", st.Index), "-c", "copy", "-f", "image2pipe", "-"}
		run, err := runTool(ctx, ffmpegPath, args, file)
		if err != nil {
			return err
		}
		pic := sha256.Sum256(run.Stdout)
		sum := hex.EncodeToString(pic[:])
		video++
		if sum != coverSHA {
			ts.CoverOK, ts.coverBad = fmt.Sprintf("stream %d: %s, the cover file is %q", st.Index, sum, coverSHA), true
		}
	}
	ts.Streams = strings.Join(desc, " ")
	if _, n := p.audio(); n != 1 {
		ts.CoverOK, ts.coverBad = fmt.Sprintf("%d audio streams", n), true
	}
	switch {
	case ts.coverBad:
	case coverSHA == "" && video == 0:
		ts.CoverOK = "no cover, no video stream"
	case coverSHA != "" && video == 1:
		ts.CoverOK = "the one video stream is the cover file, byte for byte"
	default:
		ts.CoverOK, ts.coverBad = fmt.Sprintf("%d video streams, cover %q", video, coverSHA), true
	}
	return nil
}

// editStep is one change made through MusicLib's API.
type editStep struct {
	Label string
	// Revises says whether the step must raise album_revision (DESIGN.md
	// §4.2): every change of the output does, a forced render does not.
	Revises bool
	Do      func(ctx context.Context, c *client, a album) error
}

var editSteps = []editStep{
	{"title of track 1", true, func(ctx context.Context, c *client, a album) error {
		u := a.update()
		u.Tracks[0].Title += " (edited)"
		return c.putAlbum(ctx, a, u)
	}},
	{"swap numbers 1 and 2", true, func(ctx context.Context, c *client, a album) error {
		u := a.update()
		i := slices.IndexFunc(u.Tracks, func(t trackUpdate) bool { return t.Disc == 1 && t.No == 1 })
		j := slices.IndexFunc(u.Tracks, func(t trackUpdate) bool { return t.Disc == 1 && t.No == 2 })
		if i < 0 || j < 0 {
			return errors.New("no tracks 1 and 2 on disc 1")
		}
		u.Tracks[i].No, u.Tracks[j].No = 2, 1
		return c.putAlbum(ctx, a, u)
	}},
	{"cover (JPEG)", true, func(ctx context.Context, c *client, a album) error { return putCover(ctx, c, a, "cover-edit-1.jpg") }},
	{"cover (PNG)", true, func(ctx context.Context, c *client, a album) error { return putCover(ctx, c, a, "cover-edit-2.png") }},
	{"artist rename", true, func(ctx context.Context, c *client, a album) error {
		var ar artist
		if err := c.call(ctx, http.MethodGet, "/api/artists/"+a.ArtistID, nil, nil, &ar); err != nil {
			return err
		}
		return c.call(ctx, http.MethodPut, "/api/artists/"+a.ArtistID, map[string]string{"If-Match": ar.ETag},
			map[string]string{"name": ar.Name + " Renamed"}, nil)
	}},
	{"forced render", false, func(ctx context.Context, c *client, a album) error {
		return c.call(ctx, http.MethodPost, "/api/albums/"+a.ID+"/render", map[string]string{"If-Match": a.ETag}, nil, nil)
	}},
}

func putCover(ctx context.Context, c *client, a album, name string) error {
	data, err := os.ReadFile(filepath.Join(workRoot, name))
	if err != nil {
		return err
	}
	return c.call(ctx, http.MethodPut, "/api/albums/"+a.ID+"/cover", map[string]string{"If-Match": a.ETag},
		rawBody{data: data, contentType: "application/octet-stream"}, nil)
}

// runStep applies step to the album of prev, with a library watcher running
// and a track file held open across it, waits for the render, and captures
// the new state.
func runStep(ctx context.Context, c *client, prev albumState, step editStep) (albumState, error) {
	openRel := prev.Tracks[0].Rel
	held, err := os.Open(filepath.Join(libraryRoot, filepath.FromSlash(prev.Dir.Rel), filepath.FromSlash(openRel)))
	if err != nil {
		return albumState{}, err
	}
	var w watchResult
	p, stepErr := withWatcher(ctx, &w, func() (published, error) {
		if err := step.Do(ctx, c, prev.API); err != nil {
			return published{}, err
		}
		return c.waitPublished(ctx, prev.API.ID, prev.Dir.Receipt.BuildID)
	})
	h := sha256.New()
	_, rerr := io.Copy(h, held)
	cerr := held.Close()
	if err := errors.Join(stepErr, rerr, cerr); err != nil {
		return albumState{}, fmt.Errorf("step %q on %s: %w", step.Label, prev.Dir.Rel, err)
	}
	s, err := capture(ctx, step.Label, p)
	if err != nil {
		return albumState{}, err
	}
	s.Watch = w
	if sum := hex.EncodeToString(h.Sum(nil)); sum == prev.Tracks[0].SHA256 {
		s.OpenFile = "the file " + openRel + " opened before the step still reads its old content, byte for byte"
	} else {
		s.OpenFile, s.openBad = fmt.Sprintf("the file %s opened before the step now reads %s, not its old %s", openRel, sum, prev.Tracks[0].SHA256), true
	}
	return s, nil
}

// fingerprintDiffs compares the fingerprints of two states of an album by
// MusicLib track id; it returns one line per difference (nil when every
// track kept its fingerprint).
func fingerprintDiffs(before, after albumState) []string {
	var diffs []string
	fp := map[string]string{}
	for _, t := range before.Tracks {
		fp[t.ID] = t.FP
	}
	for _, t := range after.Tracks {
		old, ok := fp[t.ID]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("track %s is new", t.ID))
		case old != t.FP:
			diffs = append(diffs, fmt.Sprintf("track %s: %s, was %s", t.ID, t.FP, old))
		}
		delete(fp, t.ID)
	}
	for id := range fp {
		diffs = append(diffs, fmt.Sprintf("track %s is gone", id))
	}
	slices.Sort(diffs)
	return diffs
}

// receiptDiffs checks the receipt of after against before (DESIGN.md §4.2,
// H5): album_revision up exactly when the step changes the output, a new
// build_id every time, render_version present and unchanged, and the
// receipt matching its folder. It returns one line per violation.
func receiptDiffs(before, after albumState, revises bool) []string {
	var diffs []string
	b, a := before.Dir.Receipt, after.Dir.Receipt
	switch {
	case revises && a.AlbumRevision <= b.AlbumRevision:
		diffs = append(diffs, fmt.Sprintf("album_revision %d after a change, was %d", a.AlbumRevision, b.AlbumRevision))
	case !revises && a.AlbumRevision != b.AlbumRevision:
		diffs = append(diffs, fmt.Sprintf("album_revision %d after a forced render, was %d", a.AlbumRevision, b.AlbumRevision))
	}
	if a.AlbumRevision != after.API.Revision {
		diffs = append(diffs, fmt.Sprintf("album_revision %d, the API says revision %d", a.AlbumRevision, after.API.Revision))
	}
	if a.BuildID == b.BuildID {
		diffs = append(diffs, "build_id unchanged: "+a.BuildID)
	}
	if a.RenderVersion == "" || a.RenderVersion != b.RenderVersion {
		diffs = append(diffs, fmt.Sprintf("render_version %q, was %q", a.RenderVersion, b.RenderVersion))
	}
	if a.AlbumID != b.AlbumID {
		diffs = append(diffs, fmt.Sprintf("album_id %s, was %s", a.AlbumID, b.AlbumID))
	}
	return append(diffs, after.Problems...)
}

// The albums of H2, one per codec (albumSpecs keys).
var codecAlbums = []struct{ Key, Codec string }{{"A", "FLAC"}, {"B", "MP3"}, {"C", "AAC"}, {"D", "ALAC"}}

// runEdits is H2 and H5 (and the per-state half of H4, the atomicity of
// §4.1 and the open-file claim): for each codec it changes the album
// through the API, step by step, and compares every state with the one
// before.
func runEdits(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var h2, h5, h4, h7 fragment
	h2.b.WriteString("## H2: the fingerprint survives every change\n\n")
	h2.para("Claim (`DESIGN.md` §5.4): the fingerprint `ffmpeg -map 0:a:0 -c copy -f hash -hash sha256 -` of a track is identical before and after a change of title, a swap of numbers, a change of cover, a rename of the artist and a forced render, for FLAC, MP3, AAC and ALAC; and it differs between tracks with different audio. Each step below is one API call to MusicLib, followed by the wait for its render; tracks are followed by MusicLib's track id.")
	h5.b.WriteString("## H5: the semantics of the receipt\n\n")
	h5.para("Claim (`DESIGN.md` §4.2): `album_revision` rises with every change and not with a forced render; `render_version` is present; `build_id` changes at every render; the receipt does not list itself; the files it lists are exactly the files on disk, with their sizes and SHA-256. The receipt of each state below was also checked byte for byte against the documented canonical form (one line, keys in order, no trailing newline).")
	h4.b.WriteString("## H4 (per state): the video streams are the cover\n\n")
	h4.para("For every file of the albums of H2, in every state: the streams as ffprobe lists them, and the comparison of the SHA-256 of each video stream's packet (`-map 0:<index> -c copy -f image2pipe -`) with the SHA-256 of the album's `cover.jpg`/`cover.png`.")
	h7.b.WriteString("## H7 (during the edits): atomic replacement and open files\n\n")
	h7.para("While each step of H2 ran, a watcher read the whole library in a loop: every album folder through a handle opened on it, its receipt, and every listed file with its size. A folder counts as incomplete only if it is still the folder at its path after the read. Before each step one track file was opened; after the render it was read to the end and hashed (`DESIGN.md` §4.1: a file already open stays readable after the replacement).")

	fpOK, recOK, coverOK, atomicOK, openOK := map[string]bool{}, true, true, true, true
	for _, ca := range codecAlbums {
		spec := specByKey(ca.Key)
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return err
		}
		p, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return err
		}
		base, err := capture(ctx, "after import", p)
		if err != nil {
			return err
		}
		states := []albumState{base}
		for _, step := range editSteps {
			s, err := runStep(ctx, c, states[len(states)-1], step)
			if err != nil {
				return err
			}
			states = append(states, s)
		}

		// H2.
		ok := true
		h2.heading("%s: album %s (%s)", ca.Codec, spec.Key, base.Dir.Rel)
		rows := [][]string{}
		for _, t := range base.Tracks {
			rows = append(rows, []string{t.ID, fmt.Sprintf("%d-%d", t.Disc, t.No), t.Title, t.FP})
		}
		h2.para("Fingerprints after the import:")
		h2.table([]string{"MusicLib track id", "disc-no", "title", "fingerprint"}, rows)
		rows = nil
		for i := 1; i < len(states); i++ {
			d := fingerprintDiffs(states[0], states[i])
			res := "every track: identical"
			if len(d) > 0 {
				ok, res = false, strings.Join(d, "; ")
			}
			rows = append(rows, []string{states[i].Label, "`" + states[i].Dir.Rel + "`", tracksLine(states[i]), res})
		}
		h2.table([]string{"after step", "folder", "tracks (disc-no title: file sha256 prefix)", "fingerprint vs. after import"}, rows)
		fpOK[ca.Codec] = ok
		h2.b.WriteString("<details><summary>Every fingerprint command of " + ca.Codec + ", with its output</summary>\n\n")
		for _, s := range states {
			for _, t := range s.Tracks {
				h2.command(t.FPRun.Cmdline, string(t.FPRun.Stdout))
			}
		}
		h2.b.WriteString("</details>\n\n")

		// H5.
		h5.heading("%s: album %s", ca.Codec, spec.Key)
		rows = nil
		for i, s := range states {
			r := s.Dir.Receipt
			check := "-"
			if i > 0 {
				d := receiptDiffs(states[i-1], s, editSteps[i-1].Revises)
				check = "as expected"
				if len(d) > 0 {
					recOK, check = false, strings.Join(d, "; ")
				}
			} else if len(s.Problems) > 0 {
				recOK, check = false, strings.Join(s.Problems, "; ")
			}
			rows = append(rows, []string{s.Label, fmt.Sprint(s.API.Revision), fmt.Sprint(r.AlbumRevision), r.BuildID,
				fmt.Sprint(len(r.Files)), selfListed(r), check})
		}
		h5.table([]string{"state", "API revision", "album_revision", "build_id", "files listed", "lists itself", "check against the state before"}, rows)
		h5.para("`render_version` in every state: `%s`.", base.Dir.Receipt.RenderVersion)
		h5.b.WriteString("<details><summary>The receipt after the forced render, as on disk</summary>\n\n")
		last := states[len(states)-1]
		raw, err := os.ReadFile(filepath.Join(libraryRoot, filepath.FromSlash(last.Dir.Rel), receiptName))
		if err != nil {
			return err
		}
		h5.command("cat "+shellQuote(path.Join(libraryRoot, last.Dir.Rel, receiptName)), string(raw))
		h5.b.WriteString("</details>\n\n")

		// H4 per state.
		h4.heading("%s: album %s", ca.Codec, spec.Key)
		rows = nil
		for _, s := range states {
			for _, t := range s.Tracks {
				if t.coverBad {
					coverOK = false
				}
				rows = append(rows, []string{s.Label, t.Rel, t.Streams, t.CoverOK})
			}
		}
		h4.table([]string{"state", "file", "streams (ffprobe)", "video streams vs. the cover file"}, rows)

		// Atomicity and open files.
		h7.heading("%s: album %s", ca.Codec, spec.Key)
		rows = nil
		for _, s := range states[1:] {
			partial := "none"
			if len(s.Watch.Partial) > 0 {
				atomicOK, partial = false, strings.Join(s.Watch.Partial, "; ")
			}
			if s.openBad {
				openOK = false
			}
			rows = append(rows, []string{s.Label, fmt.Sprint(s.Watch.Scans), fmt.Sprint(s.Watch.MaxFolders[s.API.ID]),
				revisionsLine(s.Watch.Revisions[s.API.ID]), partial, s.OpenFile})
		}
		h7.table([]string{"step", "library passes", "max folders with this album_id", "their album_revision", "incomplete folders seen", "file held open"}, rows)
	}

	// H2: different audio, different fingerprint (and the same audio, the
	// same fingerprint: album F).
	h2.heading("Different audio, different fingerprints")
	distinct, rows, err := distinctFingerprints(ctx, c, all)
	if err != nil {
		return err
	}
	h2.para("Every track of the library at the end of the edits (album F has the same audio twice on purpose):")
	h2.table([]string{"album", "file", "fingerprint"}, rows)

	for _, ca := range codecAlbums {
		if err := verdict("H2."+codecOrder(ca.Codec)+" fingerprint, "+ca.Codec, fpOK[ca.Codec],
			fmt.Sprintf("the fingerprint of every %s track is identical after each of the %d steps", ca.Codec, len(editSteps))); err != nil {
			return err
		}
	}
	if err := verdict("H2.5 fingerprint, different audio", distinct,
		"tracks with different audio have different fingerprints; the two tracks of album F (same audio) have the same"); err != nil {
		return err
	}
	if err := verdict("H4.2 embedded cover", coverOK,
		"in every state of the H2 albums, each file has one audio stream and its only video stream is an attached picture equal to cover.jpg/cover.png"); err != nil {
		return err
	}
	if err := verdict("H5 receipt", recOK,
		"album_revision rises with every change (the artist rename too) and not with a forced render; build_id new at every render; render_version present and constant; no self-listing; files equal to the disk"); err != nil {
		return err
	}
	if err := verdict("H7.1 atomic replacement", atomicOK,
		"no album folder was ever seen incomplete while MusicLib replaced albums"); err != nil {
		return err
	}
	if err := verdict("H7.2 open file", openOK,
		"a track file opened before a change still reads its old bytes after the album is replaced"); err != nil {
		return err
	}
	for name, f := range map[string]*fragment{"H2": &h2, "H4b": &h4, "H5": &h5, "H7a": &h7} {
		if err := f.save(name); err != nil {
			return err
		}
	}
	return nil
}

// distinctFingerprints reads every track of the library and reports
// whether fingerprints are equal exactly when the audio is (only the two
// tracks of album F share their audio).
func distinctFingerprints(ctx context.Context, c *client, all []album) (bool, [][]string, error) {
	byFP := map[string][]string{}
	var rows [][]string
	for _, spec := range albumSpecs {
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return false, nil, err
		}
		p, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return false, nil, err
		}
		for _, f := range p.Dir.Receipt.Files {
			if !isAudio(f.RelativePath) {
				continue
			}
			fp, _, err := fingerprint(ctx, filepath.Join(libraryRoot, filepath.FromSlash(p.Dir.Rel), filepath.FromSlash(f.RelativePath)))
			if err != nil {
				return false, nil, err
			}
			byFP[fp] = append(byFP[fp], spec.Key)
			rows = append(rows, []string{spec.Key, f.RelativePath, fp})
		}
	}
	ok := true
	for _, keys := range byFP {
		if len(keys) > 1 && !(len(keys) == 2 && keys[0] == "F" && keys[1] == "F") {
			ok = false
		}
	}
	if len(byFP) != totalTracks()-1 {
		ok = false
	}
	return ok, rows, nil
}

func totalTracks() int {
	n := 0
	for _, a := range albumSpecs {
		n += len(a.Tracks)
	}
	return n
}

func specByKey(key string) albumSpec {
	i := slices.IndexFunc(albumSpecs, func(a albumSpec) bool { return a.Key == key })
	return albumSpecs[i]
}

func codecOrder(codec string) string {
	return fmt.Sprint(slices.IndexFunc(codecAlbums, func(c struct{ Key, Codec string }) bool { return c.Codec == codec }) + 1)
}

func tracksLine(s albumState) string {
	var parts []string
	for _, t := range s.Tracks {
		parts = append(parts, fmt.Sprintf("%d-%d %s: %s", t.Disc, t.No, t.Title, t.SHA256[:12]))
	}
	return strings.Join(parts, "; ")
}

func revisionsLine(revs []int64) string {
	if len(revs) == 0 {
		return "-"
	}
	return strings.Trim(fmt.Sprint(revs), "[]")
}

func selfListed(r receipt) string {
	if slices.ContainsFunc(r.Files, func(f receiptFile) bool { return f.RelativePath == receiptName }) {
		return "yes"
	}
	return "no"
}
