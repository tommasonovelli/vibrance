package library

import (
	"cmp"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/store"
)

// What IndexAlbum computes between reading the album and writing it: the
// rows the index must have, from the rows it has, the files of the receipt
// and what the tools said of them. Nothing here touches the disk, a
// process or the database.

// maxTrackNo is the highest track number (tracks.no, DESIGN.md §5.2).
const maxTrackNo = 999

// noYear is albums.year_key of an album without a year: after every real
// year (§5.2, T9).
const noYear = 10000

// trackFile is a track file of the receipt and what is known of it.
type trackFile struct {
	audio AudioFile
	// stat is what Lstat said of the file (§6.3 step 2).
	stat fs.FileInfo
	// examined is what the tools said of the file; nil for a file that is,
	// byte for byte, one the index knows (F1), which no tool was run on.
	examined *examined
}

// containerOf is the container of a track file, which is its extension:
// Classify calls audio only the files with one of these three.
func containerOf(p string) media.Container {
	switch path.Ext(p) {
	case ".flac":
		return media.ContainerFLAC
	case ".mp3":
		return media.ContainerMP3
	}
	return media.ContainerM4A
}

// place is the disc and the number of a track: those of its tags and, for a
// tag it lacks, the N of its folder "Disc N" and the NN of its name (§5.3).
// A file that says neither is track 1 of disc 1: the columns need a value,
// and MusicLib writes both tags and both names.
func place(audio AudioFile, tags media.Tags) (disc, no int) {
	nameNo, _ := nameParts(audio.Path)
	return cmp.Or(tags.Disc, audio.Disc, 1), cmp.Or(tags.Track, nameNo, 1)
}

// nameParts reads the name of a track file as MusicLib writes it,
// "NN - Title.ext": no is NN, 0 if the name has no such prefix or NN is not
// a track number, and title is the name without the prefix and the
// extension, never empty.
func nameParts(p string) (no int, title string) {
	base := path.Base(p)
	name := stem(base)
	digits := 0
	for digits < len(name) && name[digits] >= '0' && name[digits] <= '9' {
		digits++
	}
	title = name
	if rest, ok := strings.CutPrefix(name[digits:], " - "); ok && digits > 0 {
		title = rest
		if n, err := strconv.Atoi(name[:digits]); err == nil && n >= 1 && n <= maxTrackNo {
			no = n
		}
	}
	return no, cmp.Or(names.Normalize(title), names.Normalize(name), base)
}

// oldTracks are the rows of an album as the planner reads them.
func oldTracks(rows []store.Track) []OldTrack {
	olds := make([]OldTrack, len(rows))
	for i, r := range rows {
		olds[i] = OldTrack{
			ID: r.ID, RelPath: r.RelPath, FileSHA256: r.FileSha256,
			Disc: int(r.Disc), No: int(r.No), Codec: r.Codec,
			Fingerprint: r.Fingerprint, FPVersion: r.FpVersion, Occurrence: int(r.Occurrence),
			Available: r.Available == 1,
		}
		if r.DurationMs.Valid {
			olds[i].DurationMS = &r.DurationMs.Int64
		}
	}
	return olds
}

// newFiles are the track files of a receipt as the planner reads them
// before any is examined: a path and a SHA-256, which is all F1 reads.
func newFiles(tracks []trackFile) []NewFile {
	news := make([]NewFile, len(tracks))
	for j, t := range tracks {
		news[j] = NewFile{RelPath: t.audio.Path, FileSHA256: t.audio.SHA256}
	}
	return news
}

// albumWrite is everything one indexing writes (§6.3 step 8), computed
// before the write transaction begins.
type albumWrite struct {
	// before are the rows the write was computed from. The transaction
	// writes only if they are still the rows of the album.
	before snapshot
	artist store.UpsertArtistParams
	album  store.UpsertAlbumParams
	// tracks are the rows to create and those that change; a row that
	// would be written as it is is not here.
	tracks []store.UpsertTrackParams
	gone   []store.SetTrackUnavailableParams
	// changes is false when the index already says all of this: there is
	// nothing to write.
	changes bool
}

// buildWrite turns a plan into the rows to write. now is the timestamp of
// the rows that change, in Unix milliseconds. incomplete is the text of the
// warning CodeTagsIncomplete, "" when every examined track has its tags.
//
// A file that F1 pairs with a row was not examined: the row keeps what the
// index knows of the track and takes from the receipt and from the disk
// what they say of the file. The tags such a file has of its album are not
// in its row, so for DeriveAlbum it has those the album had: the file has
// not changed since they were derived from it and from the others.
func buildWrite(c Candidate, before snapshot, tracks []trackFile, plan Plan, cover coverData, now int64) (w albumWrite, incomplete string, err error) {
	matchOf := make([]*Match, len(tracks))
	for i := range plan.Matches {
		matchOf[plan.Matches[i].New] = &plan.Matches[i]
	}
	tags := make([]TrackTags, len(tracks))
	lacking := 0
	for j, t := range tracks {
		if e := t.examined; e != nil {
			tags[j] = TrackTags{Disc: e.disc, No: e.no, Album: e.tags.Album, AlbumArtist: e.tags.AlbumArtist,
				Artist: e.tags.Artist, Genre: e.tags.Genre, Year: e.tags.Year, Compilation: e.tags.Compilation}
			if lacksTags(e.tags) {
				if lacking++; lacking == 1 {
					incomplete = t.audio.Path
				}
			}
			continue
		}
		if matchOf[j] == nil || matchOf[j].Phase != PhaseContent || before.album == nil {
			return albumWrite{}, "", fmt.Errorf("the file %q was not examined, and no row of the album has its SHA-256", t.audio.Path)
		}
		old, album := before.tracks[matchOf[j].Old], before.album
		tags[j] = TrackTags{Disc: int(old.Disc), No: int(old.No), Album: album.Album.Title, AlbumArtist: album.ArtistName,
			Artist: old.Artist, Genre: old.Genre.String, Year: int(album.Album.Year.Int64), Compilation: album.Album.Compilation == 1}
	}
	if lacking > 0 {
		incomplete = fmt.Sprintf("%d of the tracks lack a title, an artist or a number in their tags, which were taken "+
			"from the names of the files and from the album; the first is %q", lacking, incomplete)
	}
	meta := DeriveAlbum(tags)

	w.before = before
	w.artist = store.UpsertArtistParams{ID: names.ArtistID(meta.Artist), Name: meta.Artist, SortKey: names.SortKey(meta.Artist)}
	w.album = store.UpsertAlbumParams{
		ID: c.Receipt.AlbumID, ArtistID: w.artist.ID, ArtistKey: w.artist.SortKey,
		Title: meta.Title, TitleKey: names.SortKey(meta.Title),
		Year: nullPositive(int64(meta.Year)), YearKey: cmp.Or(int64(meta.Year), noYear),
		Genre: nullString(meta.Genre), Compilation: boolInt(meta.Compilation),
		RelPath: c.RelPath, AlbumRevision: c.Receipt.AlbumRevision, RenderVersion: c.Receipt.RenderVersion, ReceiptHash: c.ReceiptHash,
		CoverRel: cover.Rel, CoverSha256: cover.SHA256, CoverMime: cover.MIME, CoverSize: cover.Size, CoverMtimeNs: cover.MtimeNS,
		FirstSeenAt: now, UpdatedAt: now,
	}

	// row is what the index must say of the file j. A file that was not
	// examined continues the row old, and keeps what that row knows.
	row := func(j int, old *store.Track) store.UpsertTrackParams {
		t := tracks[j]
		var p store.UpsertTrackParams
		if t.examined == nil {
			p = trackParams(*old)
		} else {
			p = examinedParams(t, meta.Artist)
			p.AlbumID = c.Receipt.AlbumID
		}
		p.RelPath, p.FileSize, p.FileSha256 = t.audio.Path, t.audio.Size, t.audio.SHA256
		p.FileMtimeNs = t.stat.ModTime().UnixNano()
		p.LyricsRel, p.LyricsSha256 = sql.NullString{}, sql.NullString{}
		if l := t.audio.Lyrics; l != nil {
			p.LyricsRel, p.LyricsSha256 = nullString(l.Path), nullString(l.SHA256)
		}
		p.UpdatedAt = now
		return p
	}
	for _, m := range plan.Matches {
		old := before.tracks[m.Old]
		p := row(m.New, &old)
		// The id of a row never changes (I3). After F2 and F3 the row has
		// the fingerprint of the file; after F1 it keeps its own. A row
		// keeps the moment its audio was first seen.
		p.ID, p.Occurrence, p.FirstSeenAt = old.ID, int64(m.Occurrence), old.FirstSeenAt
		was := trackParams(old)
		was.UpdatedAt = now
		if old.Available == 1 && reflect.DeepEqual(p, was) {
			continue
		}
		w.tracks = append(w.tracks, p)
	}
	for _, in := range plan.Inserts {
		// The planner is pure and gives no id: a UUIDv7 reads the clock.
		id, err := uuid.NewV7()
		if err != nil {
			return albumWrite{}, "", fmt.Errorf("creating the id of a track: %w", err)
		}
		p := row(in.New, nil)
		// A new row is first seen now. If it is a track that MusicLib moved
		// from another album, the end of the cycle gives it the moment of
		// the row it replaces (commitReferences).
		p.ID, p.Occurrence, p.FirstSeenAt = id.String(), int64(in.Occurrence), now
		w.tracks = append(w.tracks, p)
	}
	for _, i := range plan.Gone {
		w.gone = append(w.gone, store.SetTrackUnavailableParams{ID: before.tracks[i].ID, UpdatedAt: now})
	}
	w.changes = len(w.tracks) > 0 || len(w.gone) > 0 || !sameAlbum(before.album, w)
	return w, incomplete, nil
}

// sameAlbum reports whether the index already has the album exactly as w
// would write it, timestamps aside.
func sameAlbum(before *store.GetIndexedAlbumRow, w albumWrite) bool {
	if before == nil || before.Album.Available != 1 || before.ArtistName != w.artist.Name {
		return false
	}
	was := albumParams(before.Album)
	was.FirstSeenAt, was.UpdatedAt = w.album.FirstSeenAt, w.album.UpdatedAt
	return reflect.DeepEqual(was, w.album)
}

// lacksTags reports whether a track lacks one of the tags that §5.3 takes
// from elsewhere when it is missing.
func lacksTags(t media.Tags) bool {
	return names.Normalize(t.Title) == "" || names.Normalize(t.Artist) == "" || t.Track == 0 || t.Disc == 0
}

// examinedParams is the row of a track file the tools examined, without
// what the receipt and the plan say of it. A track without a title has the
// one in the name of its file, and a track without an artist the artist of
// its album (§5.3).
func examinedParams(t trackFile, albumArtist string) store.UpsertTrackParams {
	e := t.examined
	_, nameTitle := nameParts(t.audio.Path)
	title := cmp.Or(names.Normalize(e.tags.Title), nameTitle)
	artist := cmp.Or(names.Normalize(e.tags.Artist), albumArtist)
	p := store.UpsertTrackParams{
		Fingerprint: e.fingerprint,
		FpVersion:   e.fpVersion,
		Disc:        int64(e.disc),
		No:          int64(e.no),
		Title:       title,
		Artist:      artist,
		TitleKey:    names.SortKey(title),
		ArtistKey:   names.SortKey(artist),
		Genre:       nullString(names.Normalize(e.tags.Genre)),
		Codec:       e.info.Codec,
		SampleRate:  int64(e.info.SampleRate),
		Channels:    int64(e.info.Channels),
		BitDepth:    nullPositive(int64(e.info.BitDepth)),
		Bitrate:     nullPositive(int64(e.info.Bitrate)),
		RgTrackGain: nullFloat(e.tags.TrackGain),
		RgTrackPeak: nullFloat(e.tags.TrackPeak),
		RgAlbumGain: nullFloat(e.tags.AlbumGain),
		RgAlbumPeak: nullFloat(e.tags.AlbumPeak),
	}
	if d := e.info.DurationMS; d != nil {
		p.DurationMs = sql.NullInt64{Int64: *d, Valid: true}
	}
	return p
}

// trackParams is a row of tracks as UpsertTrack would write it unchanged.
func trackParams(t store.Track) store.UpsertTrackParams {
	return store.UpsertTrackParams{
		ID: t.ID, AlbumID: t.AlbumID, Fingerprint: t.Fingerprint, FpVersion: t.FpVersion, Occurrence: t.Occurrence,
		Disc: t.Disc, No: t.No, Title: t.Title, Artist: t.Artist, Genre: t.Genre,
		RelPath: t.RelPath, FileSize: t.FileSize, FileMtimeNs: t.FileMtimeNs, FileSha256: t.FileSha256,
		Codec: t.Codec, SampleRate: t.SampleRate, Channels: t.Channels, BitDepth: t.BitDepth, Bitrate: t.Bitrate,
		DurationMs: t.DurationMs, LyricsRel: t.LyricsRel, LyricsSha256: t.LyricsSha256,
		RgTrackGain: t.RgTrackGain, RgTrackPeak: t.RgTrackPeak, RgAlbumGain: t.RgAlbumGain, RgAlbumPeak: t.RgAlbumPeak,
		UpdatedAt: t.UpdatedAt, TitleKey: t.TitleKey, ArtistKey: t.ArtistKey, FirstSeenAt: t.FirstSeenAt,
	}
}

// albumParams is a row of albums as UpsertAlbum would write it unchanged.
func albumParams(a store.Album) store.UpsertAlbumParams {
	return store.UpsertAlbumParams{
		ID: a.ID, ArtistID: a.ArtistID, ArtistKey: a.ArtistKey, Title: a.Title, TitleKey: a.TitleKey,
		Year: a.Year, YearKey: a.YearKey, Genre: a.Genre, Compilation: a.Compilation,
		RelPath: a.RelPath, AlbumRevision: a.AlbumRevision, RenderVersion: a.RenderVersion, ReceiptHash: a.ReceiptHash,
		CoverRel: a.CoverRel, CoverSha256: a.CoverSha256, CoverMime: a.CoverMime, CoverSize: a.CoverSize, CoverMtimeNs: a.CoverMtimeNs,
		FirstSeenAt: a.FirstSeenAt, UpdatedAt: a.UpdatedAt,
	}
}

// nullString is s, and NULL for an empty one: the optional texts of the
// schema have no empty value.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullPositive is v, and NULL for 0, which is how the tools and the tags
// say that a number is absent.
func nullPositive(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: v > 0}
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
