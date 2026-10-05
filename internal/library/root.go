package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// The names Vibrance reads in the MusicLib folder, and nothing else
// (DESIGN.md I1): the library, and two markers at the root that are only
// signals (§4.5).
const (
	libraryDir        = "library"
	maintenanceMarker = ".maintenance"
	storeMarker       = ".musiclib-store"
)

// Why the Root refuses an entry that exists.
var (
	errInvalidPath = errors.New("not a valid relative path")
	errSymlink     = errors.New("a symbolic link")
	errNotRegular  = errors.New("not a regular file")
	errNotFolder   = errors.New("not a folder")
	errReplaced    = errors.New("replaced while it was being opened")
)

// Root is the MusicLib folder (/musiclib), opened once. Every access to
// the library goes through it, and it only reads (I1).
//
// Two things confine it. os.Root resolves every path below the folder and
// never leaves it, whatever the path or a symbolic link says. And the Root
// follows no symbolic link at all: os.Root alone would follow one that
// stays inside the folder, so every folder on the way to an entry is
// checked with Lstat first, and an entry that is opened is compared with
// what Lstat saw (T11). MusicLib writes no symbolic link.
//
// The paths its methods take are relative to library/ and are validated
// with validRelPath. A caller cannot name anything outside library/: the
// two markers have their own methods.
//
// It is safe for concurrent use.
type Root struct {
	dir *os.Root
}

// OpenRoot opens dir, the folder MusicLib's data volume is mounted at. It
// is the only absolute path of the library. library/ itself may be missing
// or replaced later: it is resolved again at every call.
func OpenRoot(dir string) (*Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("library: opening the MusicLib folder: %w", err)
	}
	return &Root{dir: root}, nil
}

// Close releases the folder. Files that Open returned stay open.
func (r *Root) Close() error {
	return r.dir.Close()
}

// Maintenance reports whether the marker .maintenance exists: an offline
// rebuild or restore of MusicLib is running, and library/ may be empty or
// incomplete (§4.5).
func (r *Root) Maintenance() (bool, error) {
	return r.exists(maintenanceMarker)
}

// StorePresent reports whether the marker .musiclib-store exists: without
// it the folder is not MusicLib's data volume (§4.5).
func (r *Root) StorePresent() (bool, error) {
	return r.exists(storeMarker)
}

// LibraryPresent reports whether library/ exists. Without it nothing of the
// library can be read, and the index is kept as it is (I14): a file that is
// not there says nothing of its album then.
func (r *Root) LibraryPresent() (bool, error) {
	return r.exists(libraryDir)
}

// exists reports whether the root of the folder has an entry called name,
// of any kind. The entry is not opened.
func (r *Root) exists(name string) (bool, error) {
	_, err := r.dir.Lstat(name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// Artists lists the artist folders: the folders at the top of library/, in
// byte order. See Albums for what is left out.
func (r *Root) Artists() ([]string, error) {
	return r.folders(libraryDir)
}

// Albums lists the album folders of an artist folder, in byte order. Like
// Artists, it leaves out the files, the symbolic links, the names that
// begin with "." (§6.2) and the names that validRelPath refuses, which
// MusicLib cannot have written.
func (r *Root) Albums(artist string) ([]string, error) {
	name, err := inLibrary(artist)
	if err != nil {
		return nil, err
	}
	if strings.Contains(artist, "/") {
		return nil, &fs.PathError{Op: "list", Path: artist, Err: errInvalidPath}
	}
	return r.folders(name)
}

// ReadReceipt reads the receipt of the album folder album ("Artist/Album").
// A receipt longer than MaxReceiptBytes is not read to its end, and the
// error has the code CodeReceiptTooLarge.
func (r *Root) ReadReceipt(album string) ([]byte, error) {
	f, err := r.Open(album + "/" + ReceiptName)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxReceiptBytes+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	if len(data) > MaxReceiptBytes {
		return nil, &Error{Code: CodeReceiptTooLarge,
			Msg: fmt.Sprintf("the receipt is longer than %d bytes", MaxReceiptBytes)}
	}
	return data, nil
}

// Lstat describes the entry at rel without following it: the caller sees a
// symbolic link as one, and refuses it. A symbolic link in the folders that
// lead to rel is an error.
func (r *Root) Lstat(rel string) (fs.FileInfo, error) {
	name, err := inLibrary(rel)
	if err != nil {
		return nil, err
	}
	return r.lstat(name)
}

// Open opens the regular file at rel for reading. The caller closes it.
func (r *Root) Open(rel string) (*os.File, error) {
	name, err := inLibrary(rel)
	if err != nil {
		return nil, err
	}
	return r.open(name, false)
}

// inLibrary turns rel, a path relative to library/, into the path relative
// to the root. Client input never gets here: rel comes from a listing, a
// receipt or the database (I2).
func inLibrary(rel string) (string, error) {
	if !validRelPath(rel) {
		return "", &fs.PathError{Op: "resolve", Path: rel, Err: errInvalidPath}
	}
	return libraryDir + "/" + rel, nil
}

// lstat describes the entry at name, a valid path relative to the root,
// after checking that each folder on the way is a real folder.
func (r *Root) lstat(name string) (fs.FileInfo, error) {
	for i := 0; i < len(name); i++ {
		if name[i] != '/' {
			continue
		}
		parent, err := r.dir.Lstat(name[:i])
		if err != nil {
			return nil, err
		}
		if err := checkKind(name[:i], parent, true); err != nil {
			return nil, err
		}
	}
	return r.dir.Lstat(name)
}

// open opens the entry at name, a valid path relative to the root, which
// must be a folder (folder) or a regular file.
func (r *Root) open(name string, folder bool) (*os.File, error) {
	before, err := r.lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkKind(name, before, folder); err != nil {
		return nil, err
	}
	f, err := r.dir.Open(name)
	if err != nil {
		return nil, err
	}
	// The entry may have become a symbolic link since Lstat, and os.Root
	// follows one that stays inside the folder: what is open must be the
	// very entry that Lstat saw.
	after, err := f.Stat()
	if err == nil && !os.SameFile(before, after) {
		err = &fs.PathError{Op: "open", Path: name, Err: errReplaced}
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// checkKind refuses an entry that is not a folder (folder) or not a regular
// file, and says so when it is a symbolic link.
func checkKind(name string, info fs.FileInfo, folder bool) error {
	switch mode := info.Mode(); {
	case mode&fs.ModeSymlink != 0:
		return &fs.PathError{Op: "open", Path: name, Err: errSymlink}
	case folder && !mode.IsDir():
		return &fs.PathError{Op: "open", Path: name, Err: errNotFolder}
	case !folder && !mode.IsRegular():
		return &fs.PathError{Op: "open", Path: name, Err: errNotRegular}
	}
	return nil
}

// folders lists the folders in the folder at name, a valid path relative
// to the root.
func (r *Root) folders(name string) ([]string, error) {
	f, err := r.open(name, true)
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	if err := errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		// IsDir is false for a symbolic link, also one to a folder.
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && validRelPath(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}
