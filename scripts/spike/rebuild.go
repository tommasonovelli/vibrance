package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The offline rebuild of H7 is driven by scripts/spike.sh, which stops the
// app, runs `musiclibd rebuild` and starts the app again; these subcommands
// observe it:
//
//	snapshot           before: every album on disk, and the store id
//	watch-maintenance  during: polls /musiclib/.maintenance until SIGTERM
//	after-rebuild      after the command, app still stopped: the markers and library/
//	rebuild-check      after the app's restart: every album back as before

// snapshotAlbum is an album on disk before the rebuild.
type snapshotAlbum struct {
	Rel      string
	Revision int64
	BuildID  string
	Files    map[string]string
}

const (
	snapshotFile = "pre-rebuild.json"
	watchFile    = "maintenance-watch.json"
)

func runSnapshot(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	snap := map[string]snapshotAlbum{}
	for _, a := range all {
		p, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return err
		}
		r := p.Dir.Receipt
		snap[a.ID] = snapshotAlbum{Rel: p.Dir.Rel, Revision: r.AlbumRevision, BuildID: r.BuildID, Files: receiptFiles(r)}
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := writeWorkFile(snapshotFile, raw); err != nil {
		return err
	}
	marker, err := os.ReadFile(storeMarker)
	if err != nil {
		return err
	}
	m := storeLine.FindSubmatch(marker)
	if m == nil {
		return fmt.Errorf("%s: unexpected content %q", storeMarker, marker)
	}
	// The store id is what `musiclibd rebuild --store-id` asks for.
	return writeWorkFile("store_id", m[1])
}

// maintenanceWatch is what watch-maintenance saw.
type maintenanceWatch struct {
	Polls int64
	// Seen counts the polls that found the marker.
	Seen int64
	// Content is the marker's content the first time it was read.
	Content string
	// MinArtists and MaxArtists are the numbers of entries of library/ seen
	// while the marker was there (-1: library/ absent).
	MinArtists, MaxArtists int
	// For is the time between the first and the last poll that found it.
	For time.Duration
}

// runWatchMaintenance polls the maintenance marker as fast as it can until
// it is stopped (SIGTERM), then writes what it saw to /work.
func runWatchMaintenance(ctx context.Context) error {
	w := maintenanceWatch{MinArtists: 1 << 30, MaxArtists: -2}
	var first, last time.Time
	fmt.Println("watching " + maintenanceMarker)
	for ctx.Err() == nil {
		w.Polls++
		_, err := os.Lstat(maintenanceMarker)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if w.Seen == 0 {
			first = now
			if b, err := os.ReadFile(maintenanceMarker); err == nil {
				w.Content = string(b)
			}
		}
		w.Seen++
		last = now
		n := -1
		if entries, err := os.ReadDir(libraryRoot); err == nil {
			n = len(entries)
		}
		w.MinArtists, w.MaxArtists = min(w.MinArtists, n), max(w.MaxArtists, n)
	}
	w.For = last.Sub(first)
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	fmt.Printf("stopped after %d polls, marker seen %d times\n", w.Polls, w.Seen)
	return writeWorkFile(watchFile, raw)
}

// runAfterRebuild records, with the app still stopped after the rebuild,
// what the watcher saw and what the volume holds now.
func runAfterRebuild(_ context.Context) error {
	raw, err := os.ReadFile(filepath.Join(workRoot, watchFile))
	if err != nil {
		return err
	}
	var w maintenanceWatch
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	var f fragment
	f.heading("The offline rebuild: what a reader of the volume saw")
	if w.Seen > 0 {
		f.para("The watcher polled `%s` %d times during the command and found it in %d polls, over %s. Its content: `%q`. While it was there, `library/` had between %d and %d entries (-1: absent).",
			maintenanceMarker, w.Polls, w.Seen, w.For.Round(time.Microsecond), w.Content, w.MinArtists, w.MaxArtists)
	} else {
		f.para("The watcher polled `%s` %d times during the command and never found it.", maintenanceMarker, w.Polls)
	}
	_, merr := os.Lstat(maintenanceMarker)
	entries, lerr := os.ReadDir(libraryRoot)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	store, serr := os.ReadFile(storeMarker)
	f.para("After the command, with the app still stopped: `.maintenance` %s; `.musiclib-store` %s; `library/` %s.",
		presence(merr), presenceBytes(store, serr), listing(names, lerr))
	if err := verdict("H7.6 maintenance marker", w.Seen > 0 && strings.Contains(w.Content, "rebuild") && errors.Is(merr, fs.ErrNotExist),
		"/musiclib/.maintenance exists while an offline rebuild runs (library/ is then emptied) and is gone when it ends"); err != nil {
		return err
	}
	return f.save("H7d")
}

func presence(err error) string {
	switch {
	case err == nil:
		return "is present"
	case errors.Is(err, fs.ErrNotExist):
		return "is absent"
	}
	return "cannot be read: " + err.Error()
}

func presenceBytes(b []byte, err error) string {
	if err != nil {
		return presence(err)
	}
	return fmt.Sprintf("is present (`%q`)", b)
}

func listing(names []string, err error) string {
	switch {
	case err != nil:
		return presence(err)
	case len(names) == 0:
		return "is empty"
	}
	return fmt.Sprintf("holds %d entries (%s)", len(names), strings.Join(names, ", "))
}

// runRebuildCheck waits, after the app's restart, for MusicLib to render
// every album again, and compares the library with the snapshot.
func runRebuildCheck(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(workRoot, snapshotFile))
	if err != nil {
		return err
	}
	var snap map[string]snapshotAlbum
	if err := json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	ok := len(all) == len(snap)
	var rows [][]string
	for _, id := range slices.Sorted(maps.Keys(snap)) {
		s := snap[id]
		p, err := c.waitPublished(ctx, id, s.BuildID)
		if err != nil {
			return err
		}
		r := p.Dir.Receipt
		same := p.Dir.Rel == s.Rel && r.AlbumRevision == s.Revision && maps.Equal(receiptFiles(r), s.Files)
		ok = ok && same
		rows = append(rows, []string{id, "`" + p.Dir.Rel + "`", fmt.Sprintf("%d → %d", s.Revision, r.AlbumRevision),
			s.BuildID + " → " + r.BuildID, fmt.Sprint(maps.Equal(receiptFiles(r), s.Files))})
	}
	var f fragment
	f.heading("The offline rebuild: after the app's restart")
	f.para("The app renders every album again. For each album: its folder, `album_revision` and `build_id` (before → after), and whether the receipt lists the same files with the same SHA-256.")
	f.table([]string{"album_id", "folder", "album_revision", "build_id", "same files and SHA-256"}, rows)
	if err := verdict("H7.7 rebuild keeps the albums", ok,
		"after an offline rebuild every album comes back with the same album_id, folder, album_revision and file SHA-256, and a new build_id"); err != nil {
		return err
	}
	return f.save("H7e")
}
