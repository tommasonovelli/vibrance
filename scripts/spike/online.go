package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// storeMarker is MusicLib's store marker (DESIGN.md §4.5).
const (
	storeMarker       = musiclibRoot + "/.musiclib-store"
	maintenanceMarker = musiclibRoot + "/.maintenance"
)

var storeLine = regexp.MustCompile(`^store_id=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\n?$`)

// runOnline is the online half of H7: the store marker, the rename of an
// album (the window with two folders, DESIGN.md §4.1, §6.2), and the trash
// and restore of an album.
func runOnline(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var f fragment
	f.b.WriteString("## H7: observed behaviour of MusicLib\n\n")

	// The store marker.
	f.heading("The store marker `.musiclib-store`")
	raw, err := os.ReadFile(storeMarker)
	if err != nil {
		return err
	}
	f.para("The bytes of `%s`, as a Go string literal: `%q`.", storeMarker, raw)
	_, merr := os.Lstat(maintenanceMarker)
	f.para("`%s` exists, as a regular file of %d bytes; `.maintenance` is absent while MusicLib runs normally (`Lstat`: %v).", storeMarker, len(raw), merr)
	if err := verdict("H7.3 store marker", storeLine.Match(raw) && errors.Is(merr, os.ErrNotExist),
		"/musiclib/.musiclib-store exists in a running installation and reads store_id=<uuid>; .maintenance is absent"); err != nil {
		return err
	}

	// Rename of album F: two folders with one album_id for an instant.
	spec := specByKey("F")
	a, err := c.albumByTitle(ctx, all, spec.Title)
	if err != nil {
		return err
	}
	before, err := c.waitPublished(ctx, a.ID, "")
	if err != nil {
		return err
	}
	u := a.update()
	u.Title += " Renamed"
	var w watchResult
	after, err := withWatcher(ctx, &w, func() (published, error) {
		if err := c.putAlbum(ctx, a, u); err != nil {
			return published{}, err
		}
		return c.waitPublished(ctx, a.ID, before.Dir.Receipt.BuildID)
	})
	if err != nil {
		return err
	}
	f.heading("Renaming an album")
	f.para("`PUT /api/albums/%s` with the title `%s`, while a watcher read the library in a loop (%d passes). Before: `%s`, `album_revision` %d. After: `%s`, `album_revision` %d.",
		a.ID, u.Title, w.Scans, before.Dir.Rel, before.Dir.Receipt.AlbumRevision, after.Dir.Rel, after.Dir.Receipt.AlbumRevision)
	window := "the watcher never saw two folders with this album_id at once"
	renameOK := after.Dir.Receipt.AlbumID == a.ID && after.Dir.Rel != before.Dir.Rel && len(w.Partial) == 0
	if revs := w.Revisions[a.ID]; len(revs) > 1 {
		window = fmt.Sprintf("the watcher saw %d folders with this album_id at once, with album_revision %s: the higher one is the new folder", w.MaxFolders[a.ID], revisionsLine(revs))
		renameOK = renameOK && revs[len(revs)-1] == after.Dir.Receipt.AlbumRevision && revs[0] < revs[len(revs)-1]
	}
	f.para("Observed: %s. MusicLib installs the new folder first (`RENAME_NOREPLACE`) and then moves the old one to `work/retired/` (its `internal/publish/disk.go`), so the window exists by construction; how often a reader sees it depends on timing.", window)
	gone, err := os.Stat(filepath.Join(libraryRoot, filepath.FromSlash(before.Dir.Rel)))
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the old folder %s is still there after the rename: %v %v", before.Dir.Rel, gone, err)
	}
	if err := verdict("H7.4 album rename", renameOK,
		"the renamed album keeps its album_id in a new folder; the old folder is gone; if two folders are seen at once, the new one has the higher album_revision"); err != nil {
		return err
	}

	// Trash and restore of album E.
	spec = specByKey("E")
	if a, err = c.albumByTitle(ctx, all, spec.Title); err != nil {
		return err
	}
	before, err = c.waitPublished(ctx, a.ID, "")
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodDelete, "/api/albums/"+a.ID, map[string]string{"If-Match": a.ETag}, nil, nil); err != nil {
		return err
	}
	if err := c.waitGone(ctx, a.ID); err != nil {
		return err
	}
	trashed, err := c.album(ctx, a.ID)
	if err != nil {
		return err
	}
	inLibrary, err := findAlbum(a.ID)
	if err != nil {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/api/albums/"+a.ID+"/restore", map[string]string{"If-Match": trashed.ETag}, nil, nil); err != nil {
		return err
	}
	after, err = c.waitPublished(ctx, a.ID, before.Dir.Receipt.BuildID)
	if err != nil {
		return err
	}
	f.heading("Trash and restore")
	f.para("`DELETE /api/albums/%s` (to the trash), then `POST /api/albums/%s/restore`.", a.ID, a.ID)
	rows := [][]string{
		{"before", "`" + before.Dir.Rel + "`", fmt.Sprint(before.Dir.Receipt.AlbumRevision), before.Dir.Receipt.BuildID},
		{"in the trash", fmt.Sprintf("%d folders with this album_id", len(inLibrary)), fmt.Sprint(trashed.Revision) + " (API)", "-"},
		{"restored", "`" + after.Dir.Rel + "`", fmt.Sprint(after.Dir.Receipt.AlbumRevision), after.Dir.Receipt.BuildID},
	}
	f.table([]string{"state", "folder", "album_revision", "build_id"}, rows)
	b, r := receiptFiles(before.Dir.Receipt), receiptFiles(after.Dir.Receipt)
	same := maps.Equal(b, r)
	var fileRows [][]string
	for _, k := range sortedKeys(r) {
		fileRows = append(fileRows, []string{"`" + k + "`", r[k], fmt.Sprint(b[k] == r[k])})
	}
	f.table([]string{"file (receipt after the restore)", "sha256", "same as before the trash"}, fileRows)
	f.para("Same files with the same SHA-256 before and after: **%v**.", same)
	if err := verdict("H7.5 trash and restore", after.Dir.Receipt.AlbumID == a.ID && len(inLibrary) == 0 && same,
		"an album in the trash leaves the library; restored, it comes back with the same album_id and the same files with the same SHA-256"); err != nil {
		return err
	}
	return f.save("H7b")
}

// withWatcher runs fn while a library watcher runs, and stores what the
// watcher saw in w.
func withWatcher(ctx context.Context, w *watchResult, fn func() (published, error)) (published, error) {
	wctx, stop := context.WithCancel(ctx)
	type out struct {
		res watchResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := watchLibrary(wctx, libraryRoot)
		done <- out{res, err}
	}()
	p, err := fn()
	stop()
	o := <-done
	*w = o.res
	return p, errors.Join(err, o.err)
}

// receiptFiles maps the paths of a receipt to their SHA-256.
func receiptFiles(r receipt) map[string]string {
	m := map[string]string{}
	for _, f := range r.Files {
		m[f.RelativePath] = f.SHA256
	}
	return m
}
