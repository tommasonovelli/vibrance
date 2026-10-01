package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The fixture library (DESIGN.md §12.2), relative to the repository.
const (
	fixtureDir   = "testdata/library-v1"
	fixtureDoc   = "testdata/FIXTURE.md"
	fixtureLimit = 2 << 20 // the whole library under 2 MiB (§14, S1)
)

// runFixture copies MusicLib's library/ into testdata/library-v1/ (replacing
// it), checks the copy against every receipt and the size limit, and writes
// testdata/FIXTURE.md.
func runFixture(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	dst := filepath.Join(srcRoot, fixtureDir)
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("removing the old fixture: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := copyTree(libraryRoot, dst); err != nil {
		return err
	}
	dirs, err := scanLibrary(dst)
	if err != nil {
		return err
	}
	total, files, err := treeSize(dst)
	if err != nil {
		return err
	}
	if total >= fixtureLimit {
		return fmt.Errorf("the fixture is %d bytes, the limit is %d", total, fixtureLimit)
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var rows []string
	for _, spec := range albumSpecs {
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return err
		}
		var d *albumDir
		for i := range dirs {
			if dirs[i].Receipt.AlbumID == a.ID {
				d = &dirs[i]
			}
		}
		if d == nil {
			return fmt.Errorf("album %s (%s) is not in the copy", spec.Key, a.ID)
		}
		problems, err := checkAlbumFiles(filepath.Join(dst, filepath.FromSlash(d.Rel)), d.Receipt)
		if err != nil {
			return err
		}
		if len(problems) > 0 {
			return fmt.Errorf("the copy of %s differs from its receipt: %s", d.Rel, strings.Join(problems, "; "))
		}
		rows = append(rows, fmt.Sprintf("| %s | `%s` | `%s` | %d | %s |", spec.Key, d.Rel, a.ID, len(spec.Tracks), spec.Purpose))
	}
	if len(dirs) != len(albumSpecs) {
		return fmt.Errorf("the copy has %d albums, want %d", len(dirs), len(albumSpecs))
	}
	listing, err := treeListing(dst)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(workRoot, "meta.json"))
	if err != nil {
		return err
	}
	var meta map[string]string
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	doc := fmt.Sprintf(fixtureTemplate, meta["musiclib_image"], meta["render_version"], meta["generated_at"],
		meta["ffmpeg_version"], meta["lame"], meta["atomicparsley"], meta["debian_snapshot"],
		strings.Join(rows, "\n"), files, total, listing)
	if err := os.WriteFile(filepath.Join(srcRoot, fixtureDoc), []byte(doc), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d files, %d bytes) and %s\n", fixtureDir, files, total, fixtureDoc)
	return nil
}

const fixtureTemplate = `# The fixture library

` + "`testdata/library-v1/`" + ` is a real ` + "`library/`" + ` folder written by MusicLib (` + "`DESIGN.md`" + ` §12.2): tiny synthetic albums (a 2-second sine per track), imported and rendered by the published MusicLib, then copied byte for byte. Tests read it; tests that change albums work on a temporary copy.

- MusicLib: ` + "`%s`" + `, ` + "`render_version`" + ` ` + "`%s`" + `.
- Generated: %s, by ` + "`scripts/make-fixture-library.sh`" + `.
- Input audio: ` + "`%s`" + ` (the pinned ffmpeg); MP3 by ` + "`%s`" + ` and the M4A ReplayGain atoms by ` + "`%s`" + `, from the Debian snapshot ` + "`%s`" + `.

## Albums

| | Folder | album_id | Tracks | What it covers |
|---|---|---|---|---|
%s

Every track also carries the managed tags MusicLib writes (title, artist, album artist, album, track and disc numbers with totals, year, genre); an album with a cover has it embedded in every track as well.

## Files

%d files, %d bytes in total (the limit is 2 MiB). ` + "`.gitattributes`" + ` marks the folder ` + "`-text`" + `, so Git never changes a byte: the SHA-256 in each ` + "`.musiclib.json`" + ` must keep matching.

` + "```text" + `
%s` + "```" + `

## Regenerating it

` + "```sh" + `
scripts/make-fixture-library.sh
` + "```" + `

It needs Docker with Compose v2 and network access (the MusicLib and PostgreSQL images, the Debian snapshot). It starts MusicLib in the Compose project ` + "`vibrance-spike`" + ` with new volumes and random passwords, creates the input albums, imports them through MusicLib's API, replaces ` + "`testdata/library-v1/`" + ` and this file, and then deletes the whole project. Every run gives new ` + "`album_id`" + ` and ` + "`build_id`" + ` values (MusicLib creates them), so regenerate only on purpose, together with the tests that name them.
`

// copyTree copies the regular files and folders under src into dst, which
// must not exist. Anything else (a symbolic link) is an error.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.Mkdir(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return fmt.Errorf("%s: not a regular file or folder", p)
	})
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(fmt.Errorf("copying %s: %w", src, err), out.Close())
	}
	return out.Close()
}

// treeSize returns the total size and the number of the regular files under
// root.
func treeSize(root string) (total int64, files int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		files++
		return nil
	})
	return total, files, err
}

// treeListing lists every file under root with its size, one per line.
func treeListing(root string) (string, error) {
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%8d  %s\n", info.Size(), filepath.ToSlash(rel))
		return nil
	})
	return b.String(), err
}
