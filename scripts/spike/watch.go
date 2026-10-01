package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// watchResult is what a library watcher saw while MusicLib changed albums.
type watchResult struct {
	// Scans is how many complete passes over the library were made.
	Scans int
	// MaxFolders is, per album_id, the largest number of folders with that
	// album_id seen in one pass (2 during a rename window, DESIGN.md §4.1).
	MaxFolders map[string]int
	// Revisions is, per album_id, the album_revision values of the folders
	// seen together when there were two or more.
	Revisions map[string][]int64
	// Partial describes every album folder seen in an incomplete state: a
	// listed file missing or of another size while the folder was still the
	// one at its path. DESIGN.md §4.1 says this never happens.
	Partial []string
}

// watchLibrary scans root in a loop until ctx is done, and returns what it
// saw. Each album folder is read through an os.Root opened on it, so a
// folder that MusicLib exchanges or retires during the read keeps its
// identity: an inconsistency counts only if the same folder is still at
// its path afterwards.
func watchLibrary(ctx context.Context, root string) (watchResult, error) {
	res := watchResult{MaxFolders: map[string]int{}, Revisions: map[string][]int64{}}
	for ctx.Err() == nil {
		if err := watchPass(root, &res); err != nil {
			return res, err
		}
		res.Scans++
	}
	return res, nil
}

// watchPass is one pass of watchLibrary over root.
func watchPass(root string, res *watchResult) error {
	seen := map[string][]int64{}
	artists, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("listing %s: %w", root, err)
	}
	for _, a := range artists {
		if !a.IsDir() || strings.HasPrefix(a.Name(), ".") {
			continue
		}
		albums, err := os.ReadDir(filepath.Join(root, a.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue // an artist folder removed after its last album left
		}
		if err != nil {
			return fmt.Errorf("listing %s: %w", a.Name(), err)
		}
		for _, al := range albums {
			if !al.IsDir() || strings.HasPrefix(al.Name(), ".") {
				continue
			}
			rel := a.Name() + "/" + al.Name()
			id, rev, problem, err := checkFolder(filepath.Join(root, rel))
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if problem != "" {
				res.Partial = append(res.Partial, rel+": "+problem)
			}
			if id != "" {
				seen[id] = append(seen[id], rev)
			}
		}
	}
	for id, revs := range seen {
		if len(revs) > res.MaxFolders[id] {
			res.MaxFolders[id] = len(revs)
		}
		if len(revs) > 1 {
			slices.Sort(revs)
			res.Revisions[id] = revs
		}
	}
	return nil
}

// checkFolder reads the receipt of the album folder dir and checks that
// every listed file is there with its size. It returns the album_id and
// revision ("" when the folder vanished or was replaced while it was read)
// and a problem when the folder, still in place, is incomplete.
func checkFolder(dir string) (id string, rev int64, problem string, err error) {
	r, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0, "", nil
	}
	if err != nil {
		return "", 0, "", err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	id, rev, problem = checkOpened(dir, r)
	return id, rev, problem, nil
}

// checkOpened is checkFolder on the folder r, opened at dir.
func checkOpened(dir string, r *os.Root) (id string, rev int64, problem string) {
	raw, rerr := r.ReadFile(receiptName)
	if rerr == nil {
		rc, perr := parseReceipt(raw)
		if perr != nil {
			problem = "invalid receipt: " + perr.Error()
		} else {
			id, rev = rc.AlbumID, rc.AlbumRevision
			for _, f := range rc.Files {
				info, serr := r.Lstat(f.RelativePath)
				if serr != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
					problem = fmt.Sprintf("%s missing or of another size", f.RelativePath)
					break
				}
			}
		}
	} else {
		problem = "no receipt: " + rerr.Error()
	}
	if problem == "" {
		return id, rev, ""
	}
	// Still the same folder at dir? If not, it was exchanged or retired
	// during the read: not an incomplete state of the library.
	here, err1 := os.Stat(dir)
	mine, err2 := r.Stat(".")
	if err1 != nil || err2 != nil || !os.SameFile(here, mine) {
		return "", 0, ""
	}
	return "", 0, problem
}
