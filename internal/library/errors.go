package library

import "errors"

// The stable codes of the problems that reading a receipt, classifying its
// files and discovering the albums can find (DESIGN.md §6.5). An album
// with one of them is not indexed.
const (
	// CodeReceiptMissing: an album folder without a receipt, that is a
	// folder in library/ that MusicLib did not write.
	CodeReceiptMissing = "receipt_missing"
	// CodeReceiptInvalid: the receipt cannot be read, or it is not what
	// DESIGN.md §4.2 describes.
	CodeReceiptInvalid = "receipt_invalid"
	// CodeReceiptSchemaUnsupported: the receipt has a schema_version other
	// than 1. Nothing else of it is looked at.
	CodeReceiptSchemaUnsupported = "receipt_schema_unsupported"
	// CodeReceiptTooLarge: the receipt is longer than MaxReceiptBytes, or
	// lists more than MaxReceiptFiles files.
	CodeReceiptTooLarge = "receipt_too_large"
	// CodeListingFailed: an artist folder cannot be listed. The albums
	// below it are unknown, not absent.
	CodeListingFailed = "listing_failed"
)

// The codes of the problems that indexing an album can find (§6.3, §6.5).
// An album with one of them is not indexed: nothing of it is written.
const (
	// CodeFileMissing: a track, the cover or a lyrics file that the receipt
	// lists is not there, or is not a regular file.
	CodeFileMissing = "file_missing"
	// CodeFileSizeMismatch: such a file has not the size the receipt says.
	CodeFileSizeMismatch = "file_size_mismatch"
	// CodeProbeFailed: a track file cannot be opened, or ffprobe cannot
	// read it as the audio its name says.
	CodeProbeFailed = "probe_failed"
	// CodeFingerprintFailed: ffmpeg cannot compute the fingerprint of a
	// track file.
	CodeFingerprintFailed = "fingerprint_failed"
)

// The codes of the warnings of an album that is indexed all the same.
const (
	// CodeCoverInvalid: the cover is not a JPEG or a PNG as its name says,
	// or it is too large. The album is indexed without a cover.
	CodeCoverInvalid = "cover_invalid"
	// CodeTagsIncomplete: a track lacks its title, its artist or one of its
	// numbers, which were taken from the name of its file and from the
	// album (§5.3).
	CodeTagsIncomplete = "tags_incomplete"
)

// CodeAlbumChanged is not a problem (§6.3 step 6): the album, on disk or in
// the index, changed while it was being indexed, so nothing was written,
// and the next cycle indexes what is there then. It is never listed among
// the problems.
const CodeAlbumChanged = "album_changed_during_scan"

// Error is a problem of the library with its stable code. Its message never
// holds an absolute path.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Code returns the code of the first *Error in err's tree, otherwise "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Problem is one entry of the list of problems of a scan (§6.5).
type Problem struct {
	// RelPath is the folder the problem is about, relative to library/:
	// "Artist" or "Artist/Album".
	RelPath string
	Code    string
	Message string
}
