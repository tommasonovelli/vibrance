package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// Candidate is an album folder with a valid receipt: what indexing an
// album starts from (DESIGN.md §6.3).
type Candidate struct {
	// RelPath is "Artist/Album", as on disk, relative to library/.
	RelPath string
	Receipt Receipt
	// ReceiptHash is the receipt_hash of the bytes that were parsed.
	ReceiptHash string
}

// Discovery is what a look at library/ found.
type Discovery struct {
	// Candidates has one folder for each album_id, in the byte order of
	// RelPath.
	Candidates []Candidate
	// Problems are the folders that give no candidate, in the order of the
	// listing: by artist folder, then by album folder.
	Problems []Problem
	// Unlisted are the artist folders that could not be listed, in byte
	// order; each also has a CodeListingFailed problem.
	Unlisted []string
}

// Protects reports whether an album that the index has at albumRelPath
// ("Artist/Album") is below an artist folder that could not be listed. Such
// an album was not looked for, so it must not be declared absent (§6.2).
func (d Discovery) Protects(albumRelPath string) bool {
	for _, artist := range d.Unlisted {
		if strings.HasPrefix(albumRelPath, artist+"/") {
			return true
		}
	}
	return false
}

// errGone: the album folder went away between the listing and the reading
// of its receipt, as a rename in MusicLib does. It is no problem.
var errGone = errors.New("the folder is gone")

// Discover lists the artist folders of library/ and their album folders,
// reads the receipt of each album (P1), and keeps one folder for each
// album_id (P2), as DESIGN.md §6.2 says. It goes no deeper: the receipt
// describes the rest. It reads no database: registered tells it, for the
// album ids the index knows, the rel_path the index has for each, which
// only breaks a tie; nil is an empty index.
//
// When several folders have the same album_id, as during a rename in
// MusicLib, the one with the highest album_revision is kept; at the same
// revision, the one the index has; then the first in the byte order of
// the paths. The others give no problem.
//
// An error means that library/ itself could not be listed, or that ctx
// ended: nothing is known, and the caller must leave the index as it is.
func Discover(ctx context.Context, root *Root, registered map[string]string) (Discovery, error) {
	artists, err := root.Artists()
	if err != nil {
		return Discovery{}, fmt.Errorf("library: listing the library folder: %w", err)
	}
	var d Discovery
	chosen := map[string]int{} // album_id → index in d.Candidates
	for _, artist := range artists {
		if err := ctx.Err(); err != nil {
			return Discovery{}, err
		}
		albums, err := root.Albums(artist)
		if err != nil {
			d.Unlisted = append(d.Unlisted, artist)
			d.Problems = append(d.Problems, Problem{RelPath: artist, Code: CodeListingFailed,
				Message: "the artist folder cannot be listed: " + reason(err)})
			continue
		}
		for _, album := range albums {
			if err := ctx.Err(); err != nil {
				return Discovery{}, err
			}
			c, err := readCandidate(root, artist+"/"+album)
			switch {
			case errors.Is(err, errGone):
			case err != nil:
				d.Problems = append(d.Problems, Problem{RelPath: artist + "/" + album, Code: Code(err), Message: err.Error()})
			default:
				id := c.Receipt.AlbumID
				if i, seen := chosen[id]; !seen {
					chosen[id] = len(d.Candidates)
					d.Candidates = append(d.Candidates, c)
				} else if preferred(c, d.Candidates[i], registered[id]) {
					d.Candidates[i] = c
				}
			}
		}
	}
	// A folder that replaced another one took its place in the list.
	slices.SortFunc(d.Candidates, func(a, b Candidate) int { return strings.Compare(a.RelPath, b.RelPath) })
	return d, nil
}

// readCandidate reads and parses the receipt of the album folder rel. The
// error is errGone or an *Error with the code of the problem.
func readCandidate(root *Root, rel string) (Candidate, error) {
	data, err := root.ReadReceipt(rel)
	switch {
	case err == nil:
	case Code(err) != "":
		return Candidate{}, err
	case errors.Is(err, fs.ErrNotExist):
		if _, err := root.Lstat(rel); errors.Is(err, fs.ErrNotExist) {
			return Candidate{}, errGone
		}
		return Candidate{}, &Error{Code: CodeReceiptMissing, Msg: "the folder has no receipt (" + ReceiptName + ")"}
	default:
		return Candidate{}, &Error{Code: CodeReceiptInvalid, Msg: "the receipt cannot be read: " + reason(err)}
	}
	receipt, err := ParseReceipt(data)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{RelPath: rel, Receipt: receipt, ReceiptHash: ReceiptHash(data)}, nil
}

// preferred reports whether a is kept instead of b, two folders with the
// same album_id; registered is the rel_path the index has for that album,
// "" if it has none (§6.2).
func preferred(a, b Candidate, registered string) bool {
	if a.Receipt.AlbumRevision != b.Receipt.AlbumRevision {
		return a.Receipt.AlbumRevision > b.Receipt.AlbumRevision
	}
	if (a.RelPath == registered) != (b.RelPath == registered) {
		return a.RelPath == registered
	}
	return a.RelPath < b.RelPath
}

// reason is why an access to the library failed, without the path: a
// problem has its path in a field of its own.
func reason(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}
