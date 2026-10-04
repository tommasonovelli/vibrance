package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // the two formats of a cover (§4.4)
	_ "image/png"
	"io/fs"
	"os"
	"time"

	"vibrance/internal/media"
	"vibrance/internal/store"
)

// Media is what the indexer asks of the media adapter. *media.Tools is the
// implementation; the interface is there so that a test can count the
// processes an indexing starts, and see when it starts them.
type Media interface {
	Probe(ctx context.Context, f *os.File, c media.Container) (media.Info, error)
	Fingerprint(ctx context.Context, f *os.File, c media.Container) (fingerprint, version string, err error)
	// Version is the version of the ffmpeg that computes the fingerprints.
	Version() string
}

// CoverWarmer is told the SHA-256 of the cover of an album once the album
// is committed with a cover it did not have: the thumbnails of that cover
// can be made in the background (DESIGN.md §6.3 step 9, §9.2). Warm must
// not wait for them.
type CoverWarmer interface {
	Warm(coverSHA256 string)
}

// NoCoverWarmer is the CoverWarmer of a server that makes no thumbnail in
// advance.
type NoCoverWarmer struct{}

// Warm does nothing.
func (NoCoverWarmer) Warm(string) {}

// maxCoverPixels is the largest cover that is accepted (§4.4, §6.3 step 5):
// 40 megapixels.
const maxCoverPixels = 40_000_000

// Indexer brings one album of the library into the index (§6.3). It keeps
// no state of its own and is safe for concurrent use: several albums can be
// indexed at once, each in its own write transaction.
type Indexer struct {
	root   *Root
	store  *store.Store
	media  Media
	warmer CoverWarmer
	now    func() time.Time
}

// NewIndexer returns the indexer of the library at root, which writes the
// index of st. now is the clock of the timestamps it writes.
func NewIndexer(root *Root, st *store.Store, tools Media, warmer CoverWarmer, now func() time.Time) *Indexer {
	return &Indexer{root: root, store: st, media: tools, warmer: warmer, now: now}
}

// IndexAlbum makes the index say what the album folder of c holds, in the
// order of §6.3:
//
//  1. the files of the receipt are classified;
//  2. every track, the cover and every lyrics file must be on disk, a
//     regular file of the size the receipt says;
//  3. the rows the index has of the album are read;
//  4. the files that are not, byte for byte, files the index knows are
//     examined with ffprobe and ffmpeg; nothing is run for the others, and
//     what the index knows of them is kept;
//  5. a cover the index does not have is validated;
//  6. the receipt is read again: if it is not the one of c, MusicLib
//     replaced the album in the meantime, and what was read may belong to
//     two versions of it;
//  7. the data of the album are derived and the rows are paired with the
//     files (Reconcile);
//  8. everything is written in one write transaction: the artist, the
//     album, its tracks, its counters and its full-text rows;
//  9. the CoverWarmer is told of a new cover.
//
// Step 5 runs before step 4: a cover that cannot be opened is a problem
// that is tried again at every cycle, and it must cost no process.
//
// Nothing of the disk and no process is touched inside the transaction, and
// the album is written whole or not at all. An album that is in the index
// exactly as it is on disk is not written again.
//
// The warnings are what an album that was indexed still lacks
// (CodeCoverInvalid, CodeTagsIncomplete). An error means that nothing was
// written, and it is one of three kinds:
//
//   - an *Error with the code of a problem of the album (§6.5):
//     CodeReceiptTooLarge, CodeFileMissing, CodeFileSizeMismatch,
//     CodeProbeFailed, CodeFingerprintFailed;
//   - an *Error with CodeAlbumChanged: the album changed on disk, or its
//     rows changed in the index, while it was being indexed. It is not a
//     problem: the next cycle indexes what is there then. A problem found
//     in an album whose receipt changed in the meantime is reported this
//     way too, because it may be one of the replacement and not of the
//     album;
//   - anything else: ctx ended, or the database failed.
func (ix *Indexer) IndexAlbum(ctx context.Context, c Candidate) ([]Problem, error) {
	warnings, newCover, err := ix.index(ctx, c)
	switch {
	case err == nil:
		if newCover != "" {
			ix.warmer.Warm(newCover)
		}
		return warnings, nil
	case ctx.Err() != nil:
		// Whatever failed, it failed because the indexing was interrupted.
		if !errors.Is(err, ctx.Err()) {
			err = fmt.Errorf("%w: %v", ctx.Err(), err)
		}
		return nil, fmt.Errorf("library: indexing %q: %w", c.RelPath, err)
	case errors.Is(err, errIndexChanged):
		return nil, &Error{Code: CodeAlbumChanged, Msg: "the rows of the album changed in the index while it was being indexed"}
	case Code(err) == CodeAlbumChanged:
		return nil, err
	case Code(err) != "":
		if ix.receiptChanged(c) {
			return nil, albumReplaced()
		}
		return nil, err
	}
	return nil, fmt.Errorf("library: indexing %q: %w", c.RelPath, err)
}

func albumReplaced() error {
	return &Error{Code: CodeAlbumChanged, Msg: "the album was replaced on disk while it was being indexed"}
}

// index is IndexAlbum. newCover is the SHA-256 of the cover of the album
// when the album was written with a cover the index did not have.
func (ix *Indexer) index(ctx context.Context, c Candidate) (warnings []Problem, newCover string, err error) {
	// Step 1.
	files, err := Classify(c.Receipt)
	if err != nil {
		return nil, "", err
	}
	// Step 2.
	tracks := make([]trackFile, len(files.Audio))
	for j, audio := range files.Audio {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		tracks[j].audio = audio
		if tracks[j].stat, err = ix.present(c, audio.ReceiptFile); err != nil {
			return nil, "", err
		}
		if audio.Lyrics != nil {
			if _, err := ix.present(c, *audio.Lyrics); err != nil {
				return nil, "", err
			}
		}
	}
	var coverStat fs.FileInfo
	if files.Cover != nil {
		if coverStat, err = ix.present(c, *files.Cover); err != nil {
			return nil, "", err
		}
	}
	// Step 3.
	var before snapshot
	err = ix.store.Read(ctx, func(q *store.Queries) error {
		before, err = readSnapshot(ctx, q, c.Receipt.AlbumID)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	// Step 5, before step 4: a cover that cannot be opened stops the album,
	// and it is tried again at every cycle, so it must stop it before any
	// process is started.
	cover, coverWarning, err := ix.cover(c, files.Cover, coverStat, before.album)
	if err != nil {
		return nil, "", err
	}
	// Step 4. The files are examined one after the other: the albums are
	// what is indexed in parallel, and the Runner bounds the processes.
	olds, news := oldTracks(before.tracks), newFiles(tracks)
	for _, j := range NeedFingerprint(olds, news) {
		e, err := ix.examine(ctx, c, tracks[j].audio)
		if err != nil {
			return nil, "", err
		}
		tracks[j].examined = &e
		news[j].Disc, news[j].No = e.disc, e.no
		news[j].DurationMS, news[j].Codec, news[j].Fingerprint = e.info.DurationMS, e.info.Codec, e.fingerprint
	}
	// Step 6.
	if ix.receiptChanged(c) {
		return nil, "", albumReplaced()
	}
	// Step 7.
	plan, err := Reconcile(olds, news, ix.media.Version())
	if err != nil {
		return nil, "", fmt.Errorf("reconciling the tracks: %w", err)
	}
	w, incomplete, err := buildWrite(c, before, tracks, plan, cover, ix.now().UnixMilli())
	if err != nil {
		return nil, "", err
	}
	if coverWarning != "" {
		warnings = append(warnings, Problem{RelPath: c.RelPath, Code: CodeCoverInvalid, Message: coverWarning})
	}
	if incomplete != "" {
		warnings = append(warnings, Problem{RelPath: c.RelPath, Code: CodeTagsIncomplete, Message: incomplete})
	}
	if !w.changes {
		return warnings, "", nil
	}
	// Step 8.
	err = ix.store.WithWriteTx(ctx, func(q *store.Queries) error { return commitAlbum(ctx, q, w) })
	if err != nil {
		return nil, "", err
	}
	if cover.SHA256.Valid && (before.album == nil || before.album.Album.CoverSha256 != cover.SHA256) {
		newCover = cover.SHA256.String
	}
	return warnings, newCover, nil
}

// present checks that a file the receipt lists is in the album folder: it
// must exist, be a regular file and not a symbolic link, and have the size
// the receipt says (§6.3 step 2). It returns what Lstat says of it.
func (ix *Indexer) present(c Candidate, f ReceiptFile) (fs.FileInfo, error) {
	info, err := ix.root.Lstat(c.RelPath + "/" + f.Path)
	switch {
	case err != nil:
		return nil, &Error{Code: CodeFileMissing, Msg: fmt.Sprintf("%q is not there: %s", f.Path, reason(err))}
	case !info.Mode().IsRegular():
		return nil, &Error{Code: CodeFileMissing, Msg: fmt.Sprintf("%q is not a regular file", f.Path)}
	case info.Size() != f.Size:
		return nil, &Error{Code: CodeFileSizeMismatch,
			Msg: fmt.Sprintf("%q has %d bytes, and the receipt says %d", f.Path, info.Size(), f.Size)}
	}
	return info, nil
}

// receiptChanged reports whether the receipt of the album folder is no
// longer the one c was made from, or cannot be read: MusicLib replaced,
// renamed or removed the album since (T10).
func (ix *Indexer) receiptChanged(c Candidate) bool {
	data, err := ix.root.ReadReceipt(c.RelPath)
	return err != nil || ReceiptHash(data) != c.ReceiptHash
}

// examined is what the tools say of a track file.
type examined struct {
	info        media.Info
	tags        media.Tags
	fingerprint string
	fpVersion   string
	// disc and no are the place of the track: from its tags, or from its
	// path where it has none.
	disc, no int
}

// examine runs ffprobe and ffmpeg on a track file (§6.3 step 4). A failure
// is a problem of the album, CodeProbeFailed or CodeFingerprintFailed,
// whatever the code of the media adapter, unless ctx ended: then the error
// is that of ctx.
func (ix *Indexer) examine(ctx context.Context, c Candidate, audio AudioFile) (examined, error) {
	container := containerOf(audio.Path)
	f, err := ix.root.Open(c.RelPath + "/" + audio.Path)
	if err != nil {
		return examined{}, &Error{Code: CodeProbeFailed, Msg: fmt.Sprintf("%q cannot be opened: %s", audio.Path, reason(err))}
	}
	e, err := ix.examineOpen(ctx, f, container, audio)
	if cerr := f.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("closing %q: %s", audio.Path, reason(cerr))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return examined{}, ctxErr
	}
	return e, err
}

func (ix *Indexer) examineOpen(ctx context.Context, f *os.File, container media.Container, audio AudioFile) (examined, error) {
	info, err := ix.media.Probe(ctx, f, container)
	if err != nil {
		return examined{}, &Error{Code: CodeProbeFailed, Msg: fmt.Sprintf("%q: %v", audio.Path, err)}
	}
	fingerprint, version, err := ix.media.Fingerprint(ctx, f, container)
	if err != nil {
		return examined{}, &Error{Code: CodeFingerprintFailed, Msg: fmt.Sprintf("%q: %v", audio.Path, err)}
	}
	e := examined{info: info, tags: media.MapTags(info.Tags), fingerprint: fingerprint, fpVersion: version}
	e.disc, e.no = place(audio, e.tags)
	return e, nil
}

// coverData are the cover columns of an album; all null for an album
// without a cover.
type coverData struct {
	Rel, SHA256, MIME sql.NullString
	Size, MtimeNS     sql.NullInt64
}

// cover returns the cover of the album (§6.3 step 5). A cover the index
// already has, by its SHA-256 and its name, was validated when it was
// indexed. Any other is validated by its header alone: it must be the
// format its name says, JPEG or PNG, and of at most maxCoverPixels. One
// that is not leaves the album without a cover, and warning says why. A
// cover that cannot be opened says nothing of the image: it is a problem
// of the album (CodeFileMissing), which is not indexed in this cycle.
func (ix *Indexer) cover(c Candidate, f *ReceiptFile, stat fs.FileInfo, before *store.GetIndexedAlbumRow) (cover coverData, warning string, err error) {
	if f == nil {
		return coverData{}, "", nil
	}
	mime := ""
	if before != nil && before.Album.CoverSha256.String == f.SHA256 && before.Album.CoverRel.String == f.Path {
		mime = before.Album.CoverMime.String
	} else if mime, warning, err = ix.validCover(c, *f); err != nil || warning != "" {
		return coverData{}, warning, err
	}
	return coverData{
		Rel:     nullString(f.Path),
		SHA256:  nullString(f.SHA256),
		MIME:    nullString(mime),
		Size:    nullPositive(stat.Size()),
		MtimeNS: nullPositive(stat.ModTime().UnixNano()),
	}, "", nil
}

// validCover reads the header of a cover file and returns its MIME type, or
// why it is not a valid cover. A file that cannot be opened is an error
// and not a warning: the fault may pass, and an album indexed without its
// cover would stay so until its receipt changes.
func (ix *Indexer) validCover(c Candidate, f ReceiptFile) (mime, warning string, err error) {
	want, mime := "jpeg", "image/jpeg"
	if f.Path == "cover.png" {
		want, mime = "png", "image/png"
	}
	file, err := ix.root.Open(c.RelPath + "/" + f.Path)
	if err != nil {
		return "", "", &Error{Code: CodeFileMissing, Msg: fmt.Sprintf("%q cannot be opened: %s", f.Path, reason(err))}
	}
	// Only the header is decoded: the pixels of a cover of 40 megapixels
	// would take 160 MB.
	config, format, err := image.DecodeConfig(file)
	if cerr := file.Close(); cerr != nil && err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return "", fmt.Sprintf("%q is not a JPEG or PNG image: %s", f.Path, reason(err)), nil
	case format != want:
		return "", fmt.Sprintf("%q is a %s image", f.Path, format), nil
	case int64(config.Width)*int64(config.Height) > maxCoverPixels:
		return "", fmt.Sprintf("%q has %d x %d pixels, and the maximum is 40 megapixels", f.Path, config.Width, config.Height), nil
	}
	return mime, "", nil
}
