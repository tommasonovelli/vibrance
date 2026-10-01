package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// discover runs Discover with an empty index and fails the test on an
// error.
func discover(t *testing.T, root *Root) Discovery {
	t.Helper()
	d, err := Discover(t.Context(), root, nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return d
}

// found maps the rel_path of each candidate to its album_id, and fails the
// test if an album_id is there twice.
func found(t *testing.T, d Discovery) map[string]string {
	t.Helper()
	out := map[string]string{}
	ids := map[string]string{}
	for _, c := range d.Candidates {
		if other, twice := ids[c.Receipt.AlbumID]; twice {
			t.Fatalf("the album %s is a candidate twice: %q and %q", c.Receipt.AlbumID, other, c.RelPath)
		}
		ids[c.Receipt.AlbumID] = c.RelPath
		out[c.RelPath] = c.Receipt.AlbumID
	}
	return out
}

// without is the albums of the fixture except some.
func without(rels ...string) map[string]string {
	out := map[string]string{}
	for rel, id := range fixtureAlbums {
		if !slices.Contains(rels, rel) {
			out[rel] = id
		}
	}
	return out
}

func wantFound(t *testing.T, d Discovery, want map[string]string) {
	t.Helper()
	if got := found(t, d); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates\n%q\nwant\n%q", got, want)
	}
}

func wantProblems(t *testing.T, d Discovery, want ...Problem) {
	t.Helper()
	if !reflect.DeepEqual(d.Problems, want) {
		t.Fatalf("problems\n%+v\nwant\n%+v", d.Problems, want)
	}
}

// The library MusicLib really wrote: one candidate for each album, with
// its receipt and receipt_hash, and no problem.
func TestDiscoverFixture(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)
	d := discover(t, root)
	wantFound(t, d, fixtureAlbums)
	wantProblems(t, d)
	if d.Unlisted != nil {
		t.Errorf("unlisted artists: %q", d.Unlisted)
	}
	var order []string
	for _, c := range d.Candidates {
		order = append(order, c.RelPath)
		data := readFile(t, inLib(dir, c.RelPath+"/"+ReceiptName))
		want, err := ParseReceipt(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.Receipt, want) || c.ReceiptHash != sha256Hex(data) {
			t.Errorf("%s: the candidate does not hold the receipt of the file and its hash", c.RelPath)
		}
	}
	if !slices.IsSorted(order) {
		t.Errorf("candidates not in byte order: %q", order)
	}

	// Discovering again finds the same.
	if again := discover(t, root); !reflect.DeepEqual(again, d) {
		t.Errorf("the second discovery differs:\n%+v\n%+v", again, d)
	}
}

func TestDiscoverEmptyLibrary(t *testing.T) {
	d := discover(t, openRoot(t, newMusicLib(t)))
	if d.Candidates != nil || d.Problems != nil || d.Unlisted != nil {
		t.Fatalf("an empty library gives %+v", d)
	}
}

// The folders that give no candidate, each with the code of its problem
// (DESIGN.md §6.2, §6.5). The other albums are not affected.
func TestDiscoverProblems(t *testing.T) {
	dir := copyFixture(t)
	receipt := func(rel string) string { return inLib(dir, rel+"/"+ReceiptName) }

	// A: no receipt. A folder MusicLib did not write: no receipt either.
	if err := os.Remove(receipt(albumA)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, inLib(dir, "Downloads/New Folder/song.flac"), "x")
	// B: a receipt of another schema version.
	writeFile(t, receipt(albumB), strings.Replace(string(readFile(t, receipt(albumB))), `"schema_version":1`, `"schema_version":2`, 1))
	// C: a receipt one byte beyond 16 MiB.
	if err := os.Truncate(receipt(albumC), MaxReceiptBytes+1); err != nil {
		t.Fatal(err)
	}
	// D: a receipt that is not a receipt.
	writeFile(t, receipt(albumD), "{}")
	// E: a receipt that cannot be read.
	lock(t, receipt(albumE))
	// More albums of Foxtrot Twins: a receipt that is a folder, one that is
	// a symbolic link to a valid receipt, and an album folder that cannot
	// be entered.
	mkdir(t, receipt("Foxtrot Twins/Receipt Is A Folder"))
	putAlbum(t, dir, "Foxtrot Twins/Real", albumReceipt("0192a5f0-0000-7000-8000-00000000000a", 1))
	rename(t, receipt("Foxtrot Twins/Real"), inLib(dir, "Foxtrot Twins/real receipt"))
	if err := os.Remove(inLib(dir, "Foxtrot Twins/Real")); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../real receipt", receipt("Foxtrot Twins/Receipt Is A Link"))
	putAlbum(t, dir, "Foxtrot Twins/Locked", albumReceipt("0192a5f0-0000-7000-8000-00000000000b", 1))
	lock(t, inLib(dir, "Foxtrot Twins/Locked"))

	d := discover(t, openRoot(t, dir))
	wantFound(t, d, map[string]string{albumF: fixtureAlbums[albumF]})
	wantProblems(t, d,
		Problem{albumA, CodeReceiptMissing, "the folder has no receipt (.musiclib.json)"},
		Problem{albumB, CodeReceiptSchemaUnsupported, "the receipt has schema_version 2, and only 1 is supported"},
		Problem{albumC, CodeReceiptTooLarge, "the receipt is longer than 16777216 bytes"},
		Problem{albumD, CodeReceiptInvalid, "the receipt is not valid: schema_version is missing or is not an integer"},
		Problem{"Downloads/New Folder", CodeReceiptMissing, "the folder has no receipt (.musiclib.json)"},
		Problem{"Foxtrot Twins/Locked", CodeReceiptInvalid, "the receipt cannot be read: permission denied"},
		Problem{"Foxtrot Twins/Receipt Is A Folder", CodeReceiptInvalid, "the receipt cannot be read: not a regular file"},
		Problem{"Foxtrot Twins/Receipt Is A Link", CodeReceiptInvalid, "the receipt cannot be read: a symbolic link"},
		Problem{albumE, CodeReceiptInvalid, "the receipt cannot be read: permission denied"},
	)
	if d.Unlisted != nil {
		t.Errorf("unlisted artists: %q", d.Unlisted)
	}
}

// A receipt of exactly 16 MiB is read and parsed: the limit is the last
// byte that is accepted.
func TestDiscoverReceiptAtTheLimit(t *testing.T) {
	dir := copyFixture(t)
	path := inLib(dir, albumA+"/"+ReceiptName)
	data := readFile(t, path)
	padded := string(data) + strings.Repeat(" ", MaxReceiptBytes-len(data))
	writeFile(t, path, padded)
	root := openRoot(t, dir)
	d := discover(t, root)
	wantFound(t, d, fixtureAlbums)
	wantProblems(t, d)
	i := slices.IndexFunc(d.Candidates, func(c Candidate) bool { return c.RelPath == albumA })
	if got, want := d.Candidates[i].ReceiptHash, sha256Hex([]byte(padded)); got != want {
		t.Errorf("receipt_hash %s, want that of the 16 MiB of the file, %s", got, want)
	}

	writeFile(t, path, padded+" ")
	d = discover(t, root)
	wantFound(t, d, without(albumA))
	wantProblems(t, d, Problem{albumA, CodeReceiptTooLarge, "the receipt is longer than 16777216 bytes"})
}

// What the discovery does not look at: files, names that begin with ".",
// and anything below the album folders (§6.2). None is a problem.
func TestDiscoverIgnores(t *testing.T) {
	dir := copyFixture(t)
	hidden := albumReceipt("0192a5f0-0000-7000-8000-0000000000aa", 1)
	putAlbum(t, dir, ".trash/Deleted Album", hidden)
	putAlbum(t, dir, ".hidden artist/Album", hidden)
	putAlbum(t, dir, "Aurora Sines/.hidden album", hidden)
	putAlbum(t, dir, "Aurora Sines/.work", hidden)
	// A receipt where an artist folder or the library keeps none.
	writeFile(t, inLib(dir, ReceiptName), string(encodeReceipt(hidden)))
	writeFile(t, inLib(dir, "Aurora Sines/"+ReceiptName), string(encodeReceipt(hidden)))
	writeFile(t, inLib(dir, "notes.txt"), "x")
	writeFile(t, inLib(dir, "Aurora Sines/notes.txt"), "x")
	// An album folder inside an album folder.
	putAlbum(t, dir, albumA+"/Extras/Nested Album", hidden)
	putAlbum(t, dir, albumE+"/Disc 1", hidden)
	// An artist folder without albums.
	mkdir(t, inLib(dir, "Nobody Yet"))

	d := discover(t, openRoot(t, dir))
	wantFound(t, d, fixtureAlbums)
	wantProblems(t, d)
}

// Symbolic links are left out, wherever they point: nothing behind one is
// a candidate, and the baits outside the MusicLib folder are never read
// (§6.8, T11). Each bait is a whole album with a valid receipt and an
// album_id of its own: read, it would be a candidate.
func TestDiscoverFollowsNoSymlink(t *testing.T) {
	dir := copyFixture(t)
	outside := filepath.Join(filepath.Dir(dir), "outside")
	baitID := func(n int) string { return fmt.Sprintf("0192a5f0-0000-7000-8000-0000000000%02x", n) }
	bait := func(n int, path string) {
		t.Helper()
		writeFile(t, filepath.Join(path, ReceiptName), string(encodeReceipt(albumReceipt(baitID(n), 99))))
	}
	// Outside the MusicLib folder.
	bait(1, filepath.Join(outside, "Artist", "Album"))
	bait(2, filepath.Join(outside, "Album"))
	// Inside it, next to library/: os.Root alone would follow these.
	bait(3, filepath.Join(dir, "originals", "Artist", "Album"))
	bait(4, filepath.Join(dir, "originals", "Album"))

	// Artist folders that are links.
	symlink(t, filepath.Join(outside, "Artist"), inLib(dir, "Link Out Absolute"))
	symlink(t, "../../outside/Artist", inLib(dir, "Link Out Relative"))
	symlink(t, "../originals/Artist", inLib(dir, "Link In"))
	symlink(t, "Aurora Sines", inLib(dir, "Link To An Artist"))
	symlink(t, "nowhere", inLib(dir, "Link To Nothing"))
	// Album folders that are links.
	symlink(t, filepath.Join(outside, "Album"), inLib(dir, "Bravo Tones/Link Out Absolute"))
	symlink(t, "../../../outside/Album", inLib(dir, "Bravo Tones/Link Out Relative"))
	symlink(t, "../../originals/Album", inLib(dir, "Bravo Tones/Link In"))
	// Receipts that are links, in folders that have nothing else.
	symlink(t, filepath.Join(outside, "Album", ReceiptName), inLib(dir, "Charlie Waves/Receipt Out/"+ReceiptName))
	symlink(t, "../../../originals/Album/"+ReceiptName, inLib(dir, "Charlie Waves/Receipt In/"+ReceiptName))

	// The links lead where they say: without the Root, the baits are read.
	for _, link := range []string{"Link Out Relative/Album", "Link In/Album", "Bravo Tones/Link Out Relative", "Bravo Tones/Link In", "Charlie Waves/Receipt Out", "Charlie Waves/Receipt In"} {
		if _, err := ParseReceipt(readFile(t, inLib(dir, link+"/"+ReceiptName))); err != nil {
			t.Fatalf("the test does not reach its own bait through %q: %v", link, err)
		}
	}

	d := discover(t, openRoot(t, dir))
	wantFound(t, d, fixtureAlbums)
	wantProblems(t, d,
		Problem{"Charlie Waves/Receipt In", CodeReceiptInvalid, "the receipt cannot be read: a symbolic link"},
		Problem{"Charlie Waves/Receipt Out", CodeReceiptInvalid, "the receipt cannot be read: a symbolic link"},
	)
	for _, c := range d.Candidates {
		if c.Receipt.AlbumRevision == 99 {
			t.Errorf("a bait was read: %+v", c)
		}
	}
}

// Without library/, or with one that cannot be listed, nothing is known:
// an error, not an empty library that would make every album absent
// (§6.2).
func TestDiscoverWithoutTheLibrary(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		want    error
	}{
		{"missing", func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, libraryDir)); err != nil {
				t.Fatal(err)
			}
		}, fs.ErrNotExist},
		{"renamed", func(t *testing.T, dir string) {
			rename(t, filepath.Join(dir, libraryDir), filepath.Join(dir, "library.old"))
		}, fs.ErrNotExist},
		{"a file", func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, libraryDir)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, libraryDir), "x")
		}, errNotFolder},
		{"a symbolic link", func(t *testing.T, dir string) {
			rename(t, filepath.Join(dir, libraryDir), filepath.Join(dir, "library.real"))
			symlink(t, "library.real", filepath.Join(dir, libraryDir))
		}, errSymlink},
		{"not readable", func(t *testing.T, dir string) {
			lock(t, filepath.Join(dir, libraryDir))
		}, fs.ErrPermission},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyFixture(t)
			root := openRoot(t, dir)
			wantFound(t, discover(t, root), fixtureAlbums)
			tc.prepare(t, dir)
			d, err := Discover(t.Context(), root, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Discover: %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(d, Discovery{}) {
				t.Fatalf("a failed discovery still gives %+v", d)
			}
		})
	}
}

// library/ is resolved at every call: when MusicLib puts it back, the
// same Root finds it again.
func TestDiscoverAfterTheLibraryComesBack(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)
	rename(t, filepath.Join(dir, libraryDir), filepath.Join(dir, "library.away"))
	if _, err := Discover(t.Context(), root, nil); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Discover without library/: %v", err)
	}
	mkdir(t, filepath.Join(dir, libraryDir))
	wantFound(t, discover(t, root), map[string]string{})
	if err := os.Remove(filepath.Join(dir, libraryDir)); err != nil {
		t.Fatal(err)
	}
	rename(t, filepath.Join(dir, "library.away"), filepath.Join(dir, libraryDir))
	wantFound(t, discover(t, root), fixtureAlbums)
}

// An artist folder that cannot be listed is a problem, and what the index
// has below it is protected: it was not looked for, so it is not absent
// (§6.2). The other artists are discovered as usual.
func TestDiscoverUnlistedArtist(t *testing.T) {
	dir := copyFixture(t)
	lock(t, inLib(dir, "Bravo Tones"))
	lock(t, inLib(dir, "Écho Café"))
	d := discover(t, openRoot(t, dir))

	wantFound(t, d, without(albumB, albumE))
	wantProblems(t, d,
		Problem{"Bravo Tones", CodeListingFailed, "the artist folder cannot be listed: permission denied"},
		Problem{"Écho Café", CodeListingFailed, "the artist folder cannot be listed: permission denied"},
	)
	if want := []string{"Bravo Tones", "Écho Café"}; !reflect.DeepEqual(d.Unlisted, want) {
		t.Fatalf("unlisted %q, want %q", d.Unlisted, want)
	}
	protects := map[string]bool{
		albumB:                      true,
		albumE:                      true,
		"Bravo Tones/Another Album": true, // the index may know albums that are gone
		albumA:                      false,
		albumF:                      false,
		"Bravo Tones":               false,
		"Bravo Tones 2/Beta MP3":    false,
		"Bravo/Beta MP3":            false,
		"bravo tones/Beta MP3":      false,
		"":                          false,
	}
	for rel, want := range protects {
		if got := d.Protects(rel); got != want {
			t.Errorf("Protects(%q) = %v, want %v", rel, got, want)
		}
	}
	if (Discovery{}).Protects(albumB) {
		t.Error("a discovery with every artist listed protects an album")
	}
}

// Several folders with one album_id, as while MusicLib renames an album:
// one is kept, the others are no problem (§6.2).
func TestDiscoverSameAlbumTwice(t *testing.T) {
	const id = "0192a5f0-0000-7000-8000-0000000000cc"
	tests := []struct {
		name       string
		folders    map[string]int64 // rel_path → album_revision
		registered string
		want       string
	}{
		{"the higher revision wins, later in the listing", map[string]int64{"Artist/Old Title": 4, "Artist/Renamed": 5}, "", "Artist/Renamed"},
		{"the higher revision wins, earlier in the listing", map[string]int64{"Artist/A New Title": 5, "Artist/Old Title": 4}, "", "Artist/A New Title"},
		{"the higher revision wins over the folder of the index", map[string]int64{"Artist/Old Title": 4, "Artist/Renamed": 5}, "Artist/Old Title", "Artist/Renamed"},
		{"the higher revision wins across artists", map[string]int64{"New Artist/Title": 2, "Old Artist/Title": 1}, "Old Artist/Title", "New Artist/Title"},
		{"the higher revision wins across artists, the other way", map[string]int64{"New Artist/Title": 1, "Old Artist/Title": 2}, "", "Old Artist/Title"},
		{"the higher revision wins, and takes its own place in the order", map[string]int64{"Artist/Title": 1, "Zed/Title": 2}, "Artist/Title", "Zed/Title"},
		{"the highest of three", map[string]int64{"Artist/A": 7, "Artist/B": 9, "Other/C": 8}, "Artist/A", "Artist/B"},
		{"the same revision: the folder of the index", map[string]int64{"Artist/A Copy": 3, "Artist/Title": 3}, "Artist/Title", "Artist/Title"},
		{"the same revision: the folder of the index, listed first", map[string]int64{"Artist/A Copy": 3, "Artist/Title": 3}, "Artist/A Copy", "Artist/A Copy"},
		{"the same revision, three folders: the folder of the index", map[string]int64{"Artist/A": 3, "Artist/B": 3, "Artist/C": 3}, "Artist/B", "Artist/B"},
		{"the same revision, no folder in the index: the first path", map[string]int64{"Artist/B Copy": 3, "Artist/A Title": 3}, "", "Artist/A Title"},
		{"the same revision, the index has another folder: the first path", map[string]int64{"Artist/B Copy": 3, "Artist/A Title": 3}, "Artist/Gone", "Artist/A Title"},
		{"the same revision, three artists: the first path", map[string]int64{"C/Title": 3, "A/Title": 3, "B/Title": 3}, "", "A/Title"},
		{"a lower revision in the index does not win", map[string]int64{"Artist/A": 3, "Artist/B": 3, "Artist/C": 2}, "Artist/C", "Artist/A"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyFixture(t)
			hashes := map[string]string{}
			for rel, revision := range tc.folders {
				hashes[rel] = putAlbum(t, dir, rel, albumReceipt(id, revision))
			}
			registered := map[string]string{fixtureAlbums[albumA]: albumA}
			if tc.registered != "" {
				registered[id] = tc.registered
			}
			d, err := Discover(t.Context(), openRoot(t, dir), registered)
			if err != nil {
				t.Fatal(err)
			}
			want := without()
			want[tc.want] = id
			wantFound(t, d, want)
			wantProblems(t, d)
			i := slices.IndexFunc(d.Candidates, func(c Candidate) bool { return c.Receipt.AlbumID == id })
			if c := d.Candidates[i]; c.Receipt.AlbumRevision != tc.folders[tc.want] || c.ReceiptHash != hashes[tc.want] {
				t.Errorf("the candidate %q has revision %d and hash %s: not the receipt of its folder", c.RelPath, c.Receipt.AlbumRevision, c.ReceiptHash)
			}
			if !slices.IsSortedFunc(d.Candidates, func(a, b Candidate) int { return strings.Compare(a.RelPath, b.RelPath) }) {
				t.Errorf("candidates not in byte order")
			}
		})
	}
}

// A folder with the album_id of a fixture album and a higher revision
// replaces it, as after a rename in MusicLib that the old folder survived.
func TestDiscoverRenamedFixtureAlbum(t *testing.T) {
	dir := copyFixture(t)
	old := readFile(t, inLib(dir, albumA+"/"+ReceiptName))
	renamed := strings.Replace(string(old), `"album_revision":1,`, `"album_revision":2,`, 1)
	writeFile(t, inLib(dir, "Aurora Sines/Alpha Renamed/"+ReceiptName), renamed)

	d, err := Discover(t.Context(), openRoot(t, dir), map[string]string{fixtureAlbums[albumA]: albumA})
	if err != nil {
		t.Fatal(err)
	}
	want := without(albumA)
	want["Aurora Sines/Alpha Renamed"] = fixtureAlbums[albumA]
	wantFound(t, d, want)
	wantProblems(t, d)
}

func TestDiscoverStopsWithTheContext(t *testing.T) {
	root := openRoot(t, copyFixture(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d, err := Discover(ctx, root, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover with an ended context: %v", err)
	}
	if !reflect.DeepEqual(d, Discovery{}) {
		t.Fatalf("an interrupted discovery still gives %+v", d)
	}
}

// While MusicLib renames an album again and again, each discovery sees it
// once or not at all, never twice and never as a problem: a folder that is
// gone when its receipt is read is not a folder without a receipt. The
// other albums are always found. Several discoveries run at once on one
// Root. When the renames end, the album is found where it is.
func TestDiscoverWhileAnAlbumIsRenamed(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)
	moving := fixtureAlbums[albumB]

	done := make(chan struct{})
	last := albumB // where the album is, once the renamer has ended
	var renamer sync.WaitGroup
	renamer.Add(1)
	go func() {
		defer renamer.Done()
		// Every name is used once, as MusicLib moves a folder: a folder
		// that went away does not come back under the same name.
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			artist := []string{"Bravo Tones", "Aurora Sines", "Zulu"}[i%3]
			to := fmt.Sprintf("%s/Beta MP3 %d", artist, i)
			if err := os.MkdirAll(filepath.Dir(inLib(dir, to)), 0o755); err != nil {
				t.Errorf("renaming: %v", err)
				return
			}
			if err := os.Rename(inLib(dir, last), inLib(dir, to)); err != nil {
				t.Errorf("renaming: %v", err)
				return
			}
			last = to
		}
	}()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 60 {
				d, err := Discover(t.Context(), root, nil)
				if err != nil {
					t.Errorf("Discover: %v", err)
					return
				}
				if len(d.Problems) != 0 {
					t.Errorf("problems: %+v", d.Problems)
					return
				}
				count := 0
				others := map[string]string{}
				for _, c := range d.Candidates {
					if c.Receipt.AlbumID == moving {
						count++
					} else {
						others[c.RelPath] = c.Receipt.AlbumID
					}
				}
				if count > 1 || !reflect.DeepEqual(others, without(albumB)) {
					t.Errorf("the moving album is found %d times; the others: %q", count, others)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	renamer.Wait()

	d := discover(t, root)
	want := without(albumB)
	want[last] = moving
	wantFound(t, d, want)
	wantProblems(t, d)
}
