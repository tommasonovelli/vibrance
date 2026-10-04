package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vibrance/internal/media"
	"vibrance/internal/store"
)

// zeros overwrites a file with as many zero bytes as it has: the size the
// receipt says, and nothing a demuxer reads.
func zeros(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, info.Size()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A problem of an album (§6.5) keeps the whole album out of the index:
// nothing of it is written, whether the index is empty or already has the
// album, and the error has the code of the problem and no absolute path.
func TestIndexAlbumProblems(t *testing.T) {
	for _, tc := range []struct {
		name  string
		album string
		// harm changes the album on disk.
		harm func(t *testing.T, e *indexEnv)
		code string
		// names is the file the message must name.
		names string
	}{
		{"a track is missing", albumA, func(t *testing.T, e *indexEnv) {
			remove(t, e.inAlbum(albumA, "02 - Second Wave.flac"))
		}, CodeFileMissing, "02 - Second Wave.flac"},
		{"the lyrics are missing", albumA, func(t *testing.T, e *indexEnv) {
			remove(t, e.inAlbum(albumA, "01 - First Light.lrc"))
		}, CodeFileMissing, "01 - First Light.lrc"},
		{"the cover is missing", albumA, func(t *testing.T, e *indexEnv) {
			remove(t, e.inAlbum(albumA, "cover.jpg"))
		}, CodeFileMissing, "cover.jpg"},
		{"a track of another size", albumB, func(t *testing.T, e *indexEnv) {
			path := e.inAlbum(albumB, "01 - One.mp3")
			writeFile(t, path, string(readFile(t, path))+"x")
		}, CodeFileSizeMismatch, "01 - One.mp3"},
		{"a truncated track", albumC, func(t *testing.T, e *indexEnv) {
			path := e.inAlbum(albumC, "02 - Two.m4a")
			writeFile(t, path, string(readFile(t, path)[:1000]))
		}, CodeFileSizeMismatch, "02 - Two.m4a"},
		{"a cover of another size", albumA, func(t *testing.T, e *indexEnv) {
			writeFile(t, e.inAlbum(albumA, "cover.jpg"), "short")
		}, CodeFileSizeMismatch, "cover.jpg"},
		{"lyrics of another size", albumA, func(t *testing.T, e *indexEnv) {
			writeFile(t, e.inAlbum(albumA, "01 - First Light.lrc"), "[00:00.00]other")
		}, CodeFileSizeMismatch, "01 - First Light.lrc"},
		{"a track is a symbolic link", albumD, func(t *testing.T, e *indexEnv) {
			path := e.inAlbum(albumD, "01 - One.m4a")
			writeFile(t, e.inAlbum(albumD, "Extras/real.m4a"), string(readFile(t, path)))
			remove(t, path)
			symlink(t, "Extras/real.m4a", path)
		}, CodeFileMissing, "01 - One.m4a"},
		{"a track is a folder", albumD, func(t *testing.T, e *indexEnv) {
			path := e.inAlbum(albumD, "01 - One.m4a")
			remove(t, path)
			mkdir(t, path)
		}, CodeFileMissing, "01 - One.m4a"},
		{"a disc folder is a symbolic link", albumE, func(t *testing.T, e *indexEnv) {
			rename(t, e.inAlbum(albumE, "Disc 1"), e.inAlbum(albumE, "Extras"))
			symlink(t, "Extras", e.inAlbum(albumE, "Disc 1"))
		}, CodeFileMissing, "Disc 1/01 - Disc One Track One.flac"},
		{"a FLAC track that is not audio", albumA, func(t *testing.T, e *indexEnv) {
			zeros(t, e.inAlbum(albumA, "03 - Third_.flac"))
			rerender(t, e.dir, albumA)
		}, CodeProbeFailed, "03 - Third_.flac"},
		{"an MP3 track that is not audio", albumB, func(t *testing.T, e *indexEnv) {
			zeros(t, e.inAlbum(albumB, "02 - Two.mp3"))
			rerender(t, e.dir, albumB)
		}, CodeProbeFailed, "02 - Two.mp3"},
		{"an M4A track that is not audio", albumC, func(t *testing.T, e *indexEnv) {
			zeros(t, e.inAlbum(albumC, "01 - One.m4a"))
			rerender(t, e.dir, albumC)
		}, CodeProbeFailed, "01 - One.m4a"},
		{"an MP3 named as an M4A", albumD, func(t *testing.T, e *indexEnv) {
			writeFile(t, e.inAlbum(albumD, "02 - Two.m4a"), string(readFile(t, e.inAlbum(albumB, "01 - One.mp3"))))
			rerender(t, e.dir, albumD)
		}, CodeProbeFailed, "02 - Two.m4a"},
		{"a track that cannot be opened", albumE, func(t *testing.T, e *indexEnv) {
			path := e.inAlbum(albumE, "Disc 2/01 - Disc Two Track One.flac")
			retag(t, path, "title=Locked")
			rerender(t, e.dir, albumE)
			lock(t, path)
		}, CodeProbeFailed, "Disc 2/01 - Disc Two Track One.flac"},
	} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/indexed=%v", tc.name, indexed), func(t *testing.T) {
				e := newIndexEnv(t)
				if indexed {
					e.indexAll()
				}
				before := e.dump()
				e.warmer.take()
				tc.harm(t, e)

				warnings, err := e.ix.IndexAlbum(t.Context(), e.candidate(tc.album))
				wantCode(t, err, tc.code)
				if warnings != nil {
					t.Errorf("warnings of an album that was not indexed: %v", warnings)
				}
				if !strings.Contains(err.Error(), tc.names) {
					t.Errorf("the message does not name %q: %v", tc.names, err)
				}
				if strings.Contains(err.Error(), e.dir) || strings.Contains(err.Error(), os.TempDir()) {
					t.Errorf("the message holds an absolute path: %v", err)
				}
				e.wantUnchanged(before)
				if got := e.warmer.take(); len(got) != 0 {
					t.Errorf("covers to warm: %v", got)
				}
			})
		}
	}
}

// A receipt with more files than the limit is a problem of the album.
func TestIndexAlbumReceiptTooLarge(t *testing.T) {
	e := newIndexEnv(t)
	c := e.candidate(albumB)
	for i := len(c.Receipt.Files); i <= MaxReceiptFiles; i++ {
		c.Receipt.Files = append(c.Receipt.Files, ReceiptFile{Path: fmt.Sprintf("Extras/%05d.txt", i), SHA256: sha256Hex(nil)})
	}
	_, err := e.ix.IndexAlbum(t.Context(), c)
	wantCode(t, err, CodeReceiptTooLarge)
	e.wantUnchanged("")
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran for a receipt that was refused", n)
	}
}

// brokenFFmpeg is a tool that reports the pinned version and then fails:
// with the real ffprobe it gives files that can be probed and not
// fingerprinted.
func brokenFFmpeg(t *testing.T) *media.Tools {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\ncase \"$*\" in\n*-version*) echo 'ffmpeg version " + media.PinnedVersion +
		" Copyright (c) the test' ;;\n*) echo 'cannot read the packets' >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tools, err := media.NewTools(t.Context(), media.NewRunner(2), path, media.FFprobePath)
	if err != nil {
		t.Fatalf("NewTools: %v", err)
	}
	return tools
}

// A file ffprobe reads and ffmpeg cannot fingerprint is the problem
// fingerprint_failed, and leaves nothing written.
func TestIndexAlbumFingerprintFailed(t *testing.T) {
	e := newIndexEnvWith(t, brokenFFmpeg(t))
	_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumB))
	wantCode(t, err, CodeFingerprintFailed)
	if !strings.Contains(err.Error(), "01 - One.mp3") || strings.Contains(err.Error(), "cannot read the packets") {
		t.Fatalf("the message must name the file and not quote the tool: %v", err)
	}
	e.wantUnchanged("")
	if p, f := e.media.probes.Load(), e.media.fingerprints.Load(); p != 1 || f != 1 {
		t.Fatalf("%d probes and %d fingerprints: the album is given up at its first failure", p, f)
	}
}

// When the tools fail on a file of an album that was half examined, the
// rows of the tracks that were examined before are not written either: the
// counts of every table are what they were.
func TestIndexAlbumProbeFailureWritesNothing(t *testing.T) {
	e := newIndexEnv(t)
	e.indexAll()
	// Every track changes, and the last one is no longer audio.
	retag(t, e.inAlbum(albumA, "01 - First Light.flac"), "title=Changed One")
	retag(t, e.inAlbum(albumA, "02 - Second Wave.flac"), "title=Changed Two")
	zeros(t, e.inAlbum(albumA, "03 - Third_.flac"))
	rerender(t, e.dir, albumA)
	before := e.dump()
	e.media.reset()

	_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumA))
	wantCode(t, err, CodeProbeFailed)
	if p := e.media.probes.Load(); p != 3 {
		t.Fatalf("%d probes, want 3: the failure is at the third file", p)
	}
	e.wantUnchanged(before)
}

// swapAt makes the guard change the library when the indexer asks for the
// n-th fingerprint: in the middle of step 4, after step 2 and before step 6.
func swapAt(e *indexEnv, n int64, change func()) {
	e.media.setHook(func(kind string, i int64) {
		if kind == "fingerprint" && i == n {
			change()
		}
	})
}

// An album that MusicLib replaces while it is being indexed is given up
// without a write (T10, §6.3 step 6): what was read may belong to two
// versions of it. The next indexing, of the album that is there then,
// succeeds.
func TestIndexAlbumReplacedWhileIndexing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(t *testing.T, e *indexEnv)
	}{
		{"rendered again with the same files", func(t *testing.T, e *indexEnv) {
			rerender(t, e.dir, albumA)
		}},
		{"a track retitled", func(t *testing.T, e *indexEnv) {
			retag(t, e.inAlbum(albumA, "01 - First Light.flac"), "title=Replaced")
			rerender(t, e.dir, albumA)
		}},
		{"a track removed", func(t *testing.T, e *indexEnv) {
			remove(t, e.inAlbum(albumA, "03 - Third_.flac"))
			rerender(t, e.dir, albumA)
		}},
		{"the folder renamed", func(t *testing.T, e *indexEnv) {
			rename(t, inLib(e.dir, albumA), inLib(e.dir, "Aurora Sines/Alpha Renamed"))
		}},
		{"the receipt removed", func(t *testing.T, e *indexEnv) {
			remove(t, e.inAlbum(albumA, ReceiptName))
		}},
	} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/indexed=%v", tc.name, indexed), func(t *testing.T) {
				e := newIndexEnv(t)
				files := int64(3)
				if indexed {
					e.indexAll()
					// One file changes, so that the indexing has one
					// to examine.
					retag(t, e.inAlbum(albumA, "02 - Second Wave.flac"), "title=Second Wave, Again")
					rerender(t, e.dir, albumA)
					files = 1
				}
				before := e.dump()
				e.warmer.take()
				e.media.reset()
				// At the last fingerprint: every file was examined when
				// the album changes.
				swapAt(e, files, func() { tc.replace(t, e) })

				warnings, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumA))
				wantCode(t, err, CodeAlbumChanged)
				if warnings != nil {
					t.Errorf("warnings of an album that was not indexed: %v", warnings)
				}
				if f := e.media.fingerprints.Load(); f != files {
					t.Fatalf("%d fingerprints, want %d: the album was not replaced where the test meant", f, files)
				}
				e.wantUnchanged(before)
				if got := e.warmer.take(); len(got) != 0 {
					t.Errorf("covers to warm: %v", got)
				}
			})
		}
	}
}

// After an album was given up because it was replaced, the next indexing
// writes the album that replaced it.
func TestIndexAlbumAfterAReplacement(t *testing.T) {
	e := newIndexEnv(t)
	swapAt(e, 2, func() {
		retag(t, e.inAlbum(albumA, "03 - Third_.flac"), "title=The Replacement")
		rerender(t, e.dir, albumA)
	})
	_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumA))
	wantCode(t, err, CodeAlbumChanged)
	e.wantUnchanged("")

	e.media.setHook(nil)
	e.index(albumA)
	id := fixtureAlbums[albumA]
	if got := e.trackAt(id, "03 - Third_.flac"); got.Title != "The Replacement" {
		t.Fatalf("the track: %+v", got)
	}
	if a := e.album(id).Album; a.AlbumRevision != 2 || a.TrackCount != 3 {
		t.Fatalf("the album: %+v", a)
	}
}

// A problem found in an album whose receipt is no longer the one the
// indexing began with is not reported as a problem: it may be one of the
// replacement, which the next cycle indexes.
func TestIndexAlbumProblemOfAReplacedAlbum(t *testing.T) {
	e := newIndexEnv(t)
	c := e.candidate(albumA)
	remove(t, e.inAlbum(albumA, "02 - Second Wave.flac"))
	rerender(t, e.dir, albumA)

	_, err := e.ix.IndexAlbum(t.Context(), c)
	wantCode(t, err, CodeAlbumChanged)
	e.wantUnchanged("")
}

// If the rows of the album change in the index between the reading of step
// 3 and the commit, the plan was made from rows that are no more: nothing
// is written, and the album is indexed at the next cycle.
func TestIndexAlbumRowsChangedWhileIndexing(t *testing.T) {
	t.Run("another indexing of the album commits first", func(t *testing.T) {
		e := newIndexEnv(t)
		other := NewIndexer(e.root, e.store, realTools(t), NoCoverWarmer{}, e.clock.now)
		var committed string
		swapAt(e, 1, func() {
			if _, err := other.IndexAlbum(t.Context(), e.candidate(albumB)); err != nil {
				t.Errorf("the other indexing: %v", err)
			}
			committed = e.dump()
		})
		_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumB))
		wantCode(t, err, CodeAlbumChanged)
		if committed == "" {
			t.Fatal("the other indexing wrote nothing")
		}
		e.wantUnchanged(committed)
		if n := len(e.tracks(fixtureAlbums[albumB])); n != 2 {
			t.Fatalf("%d rows, want 2: no track twice", n)
		}
	})
	t.Run("a row is rewritten", func(t *testing.T) {
		e := newIndexEnv(t)
		e.indexAll()
		id := fixtureAlbums[albumB]
		retag(t, e.inAlbum(albumB, "01 - One.mp3"), "title=Uno")
		rerender(t, e.dir, albumB)
		e.media.reset()
		var rewritten string
		swapAt(e, 1, func() {
			// What the job of §6.6 does to a row.
			e.write(`UPDATE tracks SET fp_version = 'another' WHERE album_id = ?`, id)
			rewritten = e.dump()
		})
		_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumB))
		wantCode(t, err, CodeAlbumChanged)
		e.wantUnchanged(rewritten)

		e.media.setHook(nil)
		e.index(albumB)
		if got := e.trackAt(id, "01 - One.mp3"); got.Title != "Uno" || got.FpVersion != media.PinnedVersion {
			t.Fatalf("the track after the next indexing: %+v", got)
		}
	})
}

// A context that ends is not a problem of the album: the error is the one
// of the context, no code, and nothing is written.
func TestIndexAlbumCancelled(t *testing.T) {
	t.Run("before it begins", func(t *testing.T) {
		e := newIndexEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := e.ix.IndexAlbum(ctx, e.candidate(albumA))
		if !errors.Is(err, context.Canceled) || Code(err) != "" {
			t.Fatalf("error %v with code %q, want context.Canceled and no code", err, Code(err))
		}
		e.wantUnchanged("")
		if n := e.media.runs(); n != 0 {
			t.Fatalf("%d processes ran", n)
		}
	})
	t.Run("while a file is examined", func(t *testing.T) {
		e := newIndexEnv(t)
		e.indexAll()
		retag(t, e.inAlbum(albumA, "01 - First Light.flac"), "title=One")
		retag(t, e.inAlbum(albumA, "02 - Second Wave.flac"), "title=Two")
		rerender(t, e.dir, albumA)
		before := e.dump()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		e.media.reset()
		swapAt(e, 1, cancel)

		start := time.Now()
		_, err := e.ix.IndexAlbum(ctx, e.candidate(albumA))
		if !errors.Is(err, context.Canceled) || Code(err) != "" {
			t.Fatalf("error %v with code %q, want context.Canceled and no code", err, Code(err))
		}
		if elapsed := time.Since(start); elapsed > 30*time.Second {
			t.Fatalf("IndexAlbum returned %s after the cancellation", elapsed)
		}
		e.wantUnchanged(before)
	})
}

// A failure of the database in the middle of the commit leaves nothing
// written: the artist, the album and the tracks written before it are
// rolled back with it. The error is not a problem of the album.
func TestIndexAlbumCommitFailureWritesNothing(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprintf("indexed=%v", indexed), func(t *testing.T) {
			e := newIndexEnv(t)
			if indexed {
				e.indexAll()
			}
			retag(t, e.inAlbum(albumB, "01 - One.mp3"), "title=First Written", "album_artist=New Artist")
			retag(t, e.inAlbum(albumB, "02 - Two.mp3"), "title=Refused", "album_artist=New Artist")
			rerender(t, e.dir, albumB)
			// The test breaks the database on purpose: the second track
			// cannot be written.
			e.write(`CREATE TRIGGER refuse_insert BEFORE INSERT ON tracks WHEN NEW.title = 'Refused'
				BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`)
			e.write(`CREATE TRIGGER refuse_update BEFORE UPDATE ON tracks WHEN NEW.title = 'Refused'
				BEGIN SELECT RAISE(ABORT, 'refused by the test'); END`)
			before := e.dump()

			_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumB))
			if err == nil || Code(err) != "" || !strings.Contains(err.Error(), "refused by the test") {
				t.Fatalf("error %v with code %q, want the failure of the database and no code", err, Code(err))
			}
			if !strings.Contains(err.Error(), albumB) {
				t.Errorf("the error does not name the album: %v", err)
			}
			e.wantUnchanged(before)
			if strings.Contains(before, "New Artist") || strings.Contains(e.dump(), "First Written") {
				t.Fatal("the test is wrong: the dump holds what must not be written")
			}
			if got := e.warmer.take(); (len(got) == 1) != indexed {
				t.Errorf("covers to warm: %v, want only that of the first indexing of album A", got)
			}
		})
	}
}

// A failure to read the index is an error of the database, not a problem
// of the album, and no process runs.
func TestIndexAlbumReadFailure(t *testing.T) {
	e := newIndexEnv(t)
	e.write(`ALTER TABLE tracks RENAME TO tracks_gone`)
	_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumA))
	if err == nil || Code(err) != "" || !strings.Contains(err.Error(), albumA) {
		t.Fatalf("error %v with code %q", err, Code(err))
	}
	if n := e.media.runs(); n != 0 {
		t.Fatalf("%d processes ran", n)
	}
}

// The errors and the warnings of the indexer never quote what a tool wrote
// on its standard error, which can hold what is in a file.
func TestIndexAlbumErrorsDoNotQuoteTheTools(t *testing.T) {
	e := newIndexEnv(t)
	zeros(t, e.inAlbum(albumB, "01 - One.mp3"))
	_, err := e.ix.IndexAlbum(t.Context(), e.candidate(albumB))
	wantCode(t, err, CodeProbeFailed)
	var mediaErr *media.Error
	if errors.As(err, &mediaErr) {
		t.Fatalf("the problem carries the error of the adapter, with its standard error: %v", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("(ffprobe)")) {
		t.Fatalf("the message does not say what the adapter found: %v", err)
	}
}

// The guard of these tests does what DESIGN.md §12.6 asks of it: a tool
// that is asked for while a write transaction is open is refused, and makes
// the test that did it fail. Every test of the indexer runs behind it.
func TestGuardRefusesAToolInsideAWriteTransaction(t *testing.T) {
	e := newIndexEnv(t)
	inside := &guard{tools: realTools(t), store: e.store, wait: 300 * time.Millisecond}
	f, err := os.Open(e.inAlbum(albumB, "01 - One.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()

	// Outside a transaction the tools run.
	if _, err := inside.Probe(t.Context(), f, media.ContainerMP3); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, _, err := inside.Fingerprint(t.Context(), f, media.ContainerMP3); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if inside.violations.Load() != 0 || inside.runs() != 2 {
		t.Fatalf("%d violations and %d runs outside a transaction", inside.violations.Load(), inside.runs())
	}

	err = e.store.WithWriteTx(t.Context(), func(*store.Queries) error {
		if _, err := inside.Probe(t.Context(), f, media.ContainerMP3); !errors.Is(err, errInsideWriteTx) {
			t.Errorf("Probe inside a write transaction: %v", err)
		}
		if _, _, err := inside.Fingerprint(t.Context(), f, media.ContainerMP3); !errors.Is(err, errInsideWriteTx) {
			t.Errorf("Fingerprint inside a write transaction: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside.violations.Load() != 2 || inside.runs() != 2 {
		t.Fatalf("%d violations and %d runs: the guard must refuse both calls and run nothing",
			inside.violations.Load(), inside.runs())
	}
}

// A cover that is there and cannot be opened says nothing of the image: it
// is a problem of the album, like a missing file, and not a cover that is
// not valid. An album indexed without it would stay without a cover until
// its receipt changes, for a fault that may pass.
func TestIndexAlbumCoverThatCannotBeOpened(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprintf("indexed=%v", indexed), func(t *testing.T) {
			e := newIndexEnv(t)
			cover, sha := "cover.jpg", coverASHA256
			if indexed {
				// A cover the index does not have yet: one it has is not
				// looked at again.
				e.indexAll()
				png := pngHeader(16, 16)
				remove(t, e.inAlbum(albumA, "cover.jpg"))
				writeFile(t, e.inAlbum(albumA, "cover.png"), string(png))
				rerender(t, e.dir, albumA)
				cover, sha = "cover.png", sha256Hex(png)
			}
			before := e.dump()
			e.warmer.take()
			e.media.reset()
			if err := os.Chmod(e.inAlbum(albumA, cover), 0); err != nil {
				t.Fatal(err)
			}

			// The album is tried again at every cycle while its cover
			// cannot be opened: no attempt may start a process, not even
			// for an album whose tracks the index does not know.
			var warnings []Problem
			var err error
			for range 3 {
				warnings, err = e.ix.IndexAlbum(t.Context(), e.candidate(albumA))
				wantCode(t, err, CodeFileMissing)
			}
			if got := e.media.runs(); got != 0 {
				t.Errorf("%d processes were started for an album whose cover cannot be opened", got)
			}
			if warnings != nil {
				t.Errorf("warnings of an album that was not indexed: %v", warnings)
			}
			if !strings.Contains(err.Error(), cover) || strings.Contains(err.Error(), e.dir) {
				t.Errorf("the message must name %q and no absolute path: %v", cover, err)
			}
			e.wantUnchanged(before)
			if got := e.warmer.take(); len(got) != 0 {
				t.Errorf("covers to warm: %v", got)
			}

			// The fault passes: the album is indexed with its cover.
			if err := os.Chmod(e.inAlbum(albumA, cover), 0o644); err != nil {
				t.Fatal(err)
			}
			if warnings := e.index(albumA); len(warnings) != 0 {
				t.Fatalf("warnings: %v", warnings)
			}
			if a := e.album(fixtureAlbums[albumA]).Album; a.CoverRel.String != cover || a.CoverSha256.String != sha {
				t.Fatalf("the cover: %v %v", a.CoverRel, a.CoverSha256)
			}
			if got := e.warmer.take(); !slices.Equal(got, []string{sha}) {
				t.Fatalf("covers to warm: %v", got)
			}
		})
	}
}
