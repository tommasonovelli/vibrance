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
