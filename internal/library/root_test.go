package library

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The bytes of a file that the Root must never give: it lives outside the
// MusicLib folder, or inside it behind a symbolic link.
const bait = "BAIT: this file must never be read through the library"

// readThrough reads the file at rel through the Root.
func readThrough(t *testing.T, root *Root, rel string) []byte {
	t.Helper()
	f, err := root.Open(rel)
	if err != nil {
		t.Fatalf("Open(%q): %v", rel, err)
	}
	data, err := io.ReadAll(f)
	if err := errors.Join(err, f.Close()); err != nil {
		t.Fatalf("reading %q: %v", rel, err)
	}
	return data
}

// mustNotOpen fails the test if rel can be opened, and tells what was read.
func mustNotOpen(t *testing.T, root *Root, rel string, want error) {
	t.Helper()
	f, err := root.Open(rel)
	if err == nil {
		data, _ := io.ReadAll(f)
		_ = f.Close()
		t.Fatalf("Open(%q) succeeded and read %q", rel, data)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Open(%q): %v, want %v", rel, err, want)
	}
}

func TestOpenRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenRoot(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a folder that does not exist: %v", err)
	}
	writeFile(t, filepath.Join(dir, "file"), "x")
	if _, err := OpenRoot(filepath.Join(dir, "file")); err == nil {
		t.Error("a file was opened as the MusicLib folder")
	}
	// library/ may be missing when the folder is opened (DESIGN.md I14).
	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Artists(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Artists without library/: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed Root answers with an error, it does not read.
	if _, err := root.Artists(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Artists after Close: %v", err)
	}
	if _, err := root.StorePresent(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("StorePresent after Close: %v", err)
	}
	if _, err := root.Open("a/b"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Open after Close: %v", err)
	}
}

// The two markers are signals: only their existence counts (§4.5).
func TestRootMarkers(t *testing.T) {
	dir := newMusicLib(t)
	root := openRoot(t, dir)
	check := func(wantMaintenance, wantStore bool) {
		t.Helper()
		maintenance, err := root.Maintenance()
		if err != nil || maintenance != wantMaintenance {
			t.Fatalf("Maintenance: %v, %v; want %v", maintenance, err, wantMaintenance)
		}
		store, err := root.StorePresent()
		if err != nil || store != wantStore {
			t.Fatalf("StorePresent: %v, %v; want %v", store, err, wantStore)
		}
	}
	check(false, true)
	writeFile(t, filepath.Join(dir, maintenanceMarker), "")
	check(true, true)
	if err := os.Remove(filepath.Join(dir, storeMarker)); err != nil {
		t.Fatal(err)
	}
	check(true, false)
	if err := os.Remove(filepath.Join(dir, maintenanceMarker)); err != nil {
		t.Fatal(err)
	}
	check(false, false)
	// A marker in library/ is not a marker.
	writeFile(t, inLib(dir, maintenanceMarker), "")
	writeFile(t, inLib(dir, storeMarker), "")
	check(false, false)
	// A marker is not read or followed: a link to nothing still exists.
	symlink(t, "nowhere", filepath.Join(dir, maintenanceMarker))
	check(true, false)
}

func TestRootListsTheFixture(t *testing.T) {
	root := openRoot(t, copyFixture(t))
	artists, err := root.Artists()
	if err != nil {
		t.Fatal(err)
	}
	// In byte order: "É" (0xC3) is after every ASCII letter.
	wantArtists := []string{"Aurora Sines", "Bravo Tones", "Charlie Waves", "Delta Pulse", "Foxtrot Twins", "Écho Café"}
	if !reflect.DeepEqual(artists, wantArtists) {
		t.Fatalf("artists %q, want %q", artists, wantArtists)
	}
	var albums []string
	for _, artist := range artists {
		names, err := root.Albums(artist)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			albums = append(albums, artist+"/"+name)
		}
	}
	wantAlbums := []string{albumA, albumB, albumC, albumD, albumF, albumE}
	if !reflect.DeepEqual(albums, wantAlbums) {
		t.Fatalf("albums %q, want %q", albums, wantAlbums)
	}
}

// A listing gives folders with a name MusicLib can have written, and
// nothing else (§6.2).
func TestRootListingLeavesOut(t *testing.T) {
	dir := newMusicLib(t)
	lib := filepath.Join(dir, libraryDir)
	for _, name := range []string{"Artist", "Other Artist", "Ünïcödé 漢字"} {
		mkdir(t, filepath.Join(lib, name))
	}
	for _, name := range []string{".hidden", ".trash", "..hidden", "...", `Back\slash`, "Not UTF-8 \xff"} {
		mkdir(t, filepath.Join(lib, name, "Album"))
	}
	writeFile(t, filepath.Join(lib, "a file"), "x")
	writeFile(t, filepath.Join(lib, ".musiclib.json"), "x")
	symlink(t, "Artist", filepath.Join(lib, "Link To Artist"))
	symlink(t, "a file", filepath.Join(lib, "Link To File"))
	symlink(t, "nowhere", filepath.Join(lib, "Link To Nothing"))

	mkdir(t, filepath.Join(lib, "Artist", "Album"))
	mkdir(t, filepath.Join(lib, "Artist", "Another Album"))
	mkdir(t, filepath.Join(lib, "Artist", ".hidden"))
	writeFile(t, filepath.Join(lib, "Artist", "notes.txt"), "x")
	symlink(t, "Album", filepath.Join(lib, "Artist", "Link To Album"))

	root := openRoot(t, dir)
	artists, err := root.Artists()
	if want := []string{"Artist", "Other Artist", "Ünïcödé 漢字"}; err != nil || !reflect.DeepEqual(artists, want) {
		t.Fatalf("artists %q, %v; want %q", artists, err, want)
	}
	albums, err := root.Albums("Artist")
	if want := []string{"Album", "Another Album"}; err != nil || !reflect.DeepEqual(albums, want) {
		t.Fatalf("albums %q, %v; want %q", albums, err, want)
	}
	albums, err = root.Albums("Other Artist")
	if err != nil || len(albums) != 0 {
		t.Fatalf("albums of an empty folder: %q, %v", albums, err)
	}
}

func TestRootListingErrors(t *testing.T) {
	dir := newMusicLib(t)
	lib := filepath.Join(dir, libraryDir)
	mkdir(t, filepath.Join(lib, "Artist", "Album"))
	mkdir(t, filepath.Join(lib, "Locked", "Album"))
	writeFile(t, filepath.Join(lib, "a file"), "x")
	symlink(t, "Artist", filepath.Join(lib, "Link"))
	lock(t, filepath.Join(lib, "Locked"))
	root := openRoot(t, dir)

	tests := []struct {
		artist string
		want   error
	}{
		{"Missing", fs.ErrNotExist},
		{"Locked", fs.ErrPermission},
		{"a file", errNotFolder},
		{"Link", errSymlink},
		{"Artist/Album", errInvalidPath},
		{"", errInvalidPath},
		{".", errInvalidPath},
		{"..", errInvalidPath},
		{"../library", errInvalidPath},
		{"/", errInvalidPath},
	}
	for _, tc := range tests {
		albums, err := root.Albums(tc.artist)
		if !errors.Is(err, tc.want) || albums != nil {
			t.Errorf("Albums(%q): %q, %v; want %v", tc.artist, albums, err, tc.want)
		}
	}
}

func TestRootLstatAndOpen(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)
	for _, rel := range []string{
		albumA + "/01 - First Light.flac",
		albumA + "/01 - First Light.lrc",
		albumA + "/cover.jpg",
		albumB + "/02 - Two.mp3",
		albumE + "/Disc 2/01 - Disc Two Track One.flac",
		albumF + "/" + ReceiptName,
	} {
		want := readFile(t, inLib(dir, rel))
		info, err := root.Lstat(rel)
		if err != nil {
			t.Fatalf("Lstat(%q): %v", rel, err)
		}
		if !info.Mode().IsRegular() || info.Size() != int64(len(want)) {
			t.Errorf("Lstat(%q): mode %v, size %d; want a regular file of %d bytes", rel, info.Mode(), info.Size(), len(want))
		}
		if got := readThrough(t, root, rel); !bytes.Equal(got, want) {
			t.Errorf("Open(%q) read %d bytes that are not those of the file", rel, len(got))
		}
	}

	// A folder is described, and is not opened as a file.
	info, err := root.Lstat(albumE + "/Disc 1")
	if err != nil || !info.IsDir() {
		t.Fatalf("Lstat of a folder: %v, %v", info, err)
	}
	mustNotOpen(t, root, albumE+"/Disc 1", errNotRegular)
	mustNotOpen(t, root, albumE, errNotRegular)

	// What is not there.
	for _, rel := range []string{"Nobody", "Nobody/Album/01.flac", albumA + "/00 - Missing.flac", albumA + "/Disc 1/01.flac"} {
		if _, err := root.Lstat(rel); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Lstat(%q): %v", rel, err)
		}
		mustNotOpen(t, root, rel, fs.ErrNotExist)
	}
	// A file where a folder is expected.
	if _, err := root.Lstat(albumA + "/cover.jpg/x"); !errors.Is(err, errNotFolder) {
		t.Errorf("Lstat below a file: %v", err)
	}
	mustNotOpen(t, root, albumA+"/cover.jpg/x", errNotFolder)
}

// A path that is not a valid relative path never reaches the disk: the
// bait it would lead to is real, in and out of the MusicLib folder.
func TestRootRefusesInvalidPaths(t *testing.T) {
	dir := copyFixture(t)
	writeFile(t, filepath.Join(dir, "secret"), bait)
	writeFile(t, filepath.Join(filepath.Dir(dir), "secret"), bait)
	writeFile(t, filepath.Join(dir, ReceiptName), bait)
	root := openRoot(t, dir)

	paths := []string{
		"", ".", "..", "/", "../secret", "../../secret", "../" + storeMarker, "../library/" + albumA + "/cover.jpg",
		albumA + "/../../../secret", albumA + "/../../../../secret", albumA + "/..", albumA + "/.", albumA + "/./cover.jpg",
		albumA + "//cover.jpg", albumA + "/", "/" + albumA + "/cover.jpg", "/etc/passwd", filepath.Join(dir, "secret"),
		`..\secret`, albumA + `\cover.jpg`, `Aurora Sines\Alpha_ Light_/cover.jpg`, albumA + "/cover.jpg\x00", "\x00",
		albumA + "/cover\xff.jpg",
	}
	for _, rel := range paths {
		if _, err := root.Lstat(rel); !errors.Is(err, errInvalidPath) {
			t.Errorf("Lstat(%q): %v", rel, err)
		}
		mustNotOpen(t, root, rel, errInvalidPath)
		if data, err := root.ReadReceipt(rel); !errors.Is(err, errInvalidPath) {
			t.Errorf("ReadReceipt(%q): %q, %v", rel, data, err)
		}
		if names, err := root.Albums(rel); !errors.Is(err, errInvalidPath) {
			t.Errorf("Albums(%q): %q, %v", rel, names, err)
		}
	}
}

// No symbolic link is followed, whether it stays in the MusicLib folder,
// where os.Root alone would follow it, or leaves it (T11, §6.8).
func TestRootFollowsNoSymlink(t *testing.T) {
	dir := copyFixture(t)
	outside := filepath.Join(filepath.Dir(dir), "outside")
	// Baits: outside the MusicLib folder, inside it next to library/, and
	// in library/ itself.
	writeFile(t, filepath.Join(outside, "bait.flac"), bait)
	writeFile(t, filepath.Join(outside, "Album", "bait.flac"), bait)
	writeFile(t, filepath.Join(dir, "originals", "bait.flac"), bait)
	writeFile(t, filepath.Join(dir, "originals", "Album", "bait.flac"), bait)
	writeFile(t, inLib(dir, "Baits/Album/bait.flac"), bait)

	album := inLib(dir, albumA)
	links := map[string]string{
		// A file that is a link.
		"to a file outside, absolute":   filepath.Join(outside, "bait.flac"),
		"to a file outside, relative":   "../../../../outside/bait.flac",
		"to a file next to library":     "../../../originals/bait.flac",
		"to a file of the library":      "../../Baits/Album/bait.flac",
		"to a file of the same folder":  "cover.jpg",
		"to nothing":                    "nowhere",
		"to itself":                     "to itself",
		"to a folder of the library":    "../../Baits/Album",
		"to the root of the filesystem": "/",
	}
	for name, target := range links {
		symlink(t, target, filepath.Join(album, name))
	}
	// A folder on the way that is a link.
	folders := map[string]string{
		"folder outside, absolute": filepath.Join(outside, "Album"),
		"folder outside, relative": "../../../../outside/Album",
		"folder next to library":   "../../../originals/Album",
		"folder of the library":    "../../Baits/Album",
	}
	for name, target := range folders {
		symlink(t, target, filepath.Join(album, name))
	}
	// An artist folder and an album folder that are links.
	symlink(t, filepath.Join(outside), inLib(dir, "Outside Artist"))
	symlink(t, "Baits", inLib(dir, "Linked Artist"))
	symlink(t, "../Baits/Album", inLib(dir, "Aurora Sines/Linked Album"))

	root := openRoot(t, dir)
	for name := range links {
		rel := albumA + "/" + name
		// Lstat shows the link as a link, for the caller to refuse.
		info, err := root.Lstat(rel)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("Lstat(%q): %v, %v; want a symbolic link", rel, info, err)
		}
		mustNotOpen(t, root, rel, errSymlink)
	}
	below := []string{}
	for name := range folders {
		below = append(below, albumA+"/"+name+"/bait.flac")
	}
	below = append(below, "Outside Artist/Album/bait.flac", "Linked Artist/Album/bait.flac", "Aurora Sines/Linked Album/bait.flac")
	for _, rel := range below {
		if info, err := root.Lstat(rel); !errors.Is(err, errSymlink) {
			t.Errorf("Lstat(%q): %v, %v; want the refusal of a symbolic link", rel, info, err)
		}
		mustNotOpen(t, root, rel, errSymlink)
	}
	for _, artist := range []string{"Outside Artist", "Linked Artist"} {
		if names, err := root.Albums(artist); !errors.Is(err, errSymlink) {
			t.Errorf("Albums(%q): %q, %v", artist, names, err)
		}
	}
	// The baits are where the links say: only the Root keeps away.
	if got := readFile(t, filepath.Join(album, "folder next to library", "bait.flac")); string(got) != bait {
		t.Fatalf("the test does not reach its own bait: %q", got)
	}
	// The real file next to the links is still served.
	if got := readThrough(t, root, albumA+"/cover.jpg"); !bytes.Equal(got, readFile(t, filepath.Join(album, "cover.jpg"))) {
		t.Error("cover.jpg is not read as it is")
	}
}

// library/ itself must be a real folder.
func TestRootLibraryIsALink(t *testing.T) {
	for name, target := range map[string]string{"inside": "elsewhere", "outside": "../outside"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "musiclib")
			for _, real := range []string{filepath.Join(dir, "elsewhere"), filepath.Join(filepath.Dir(dir), "outside")} {
				writeFile(t, filepath.Join(real, "Artist", "Album", "bait.flac"), bait)
			}
			symlink(t, target, filepath.Join(dir, libraryDir))
			root := openRoot(t, dir)
			if names, err := root.Artists(); !errors.Is(err, errSymlink) {
				t.Errorf("Artists: %q, %v", names, err)
			}
			if names, err := root.Albums("Artist"); !errors.Is(err, errSymlink) {
				t.Errorf("Albums: %q, %v", names, err)
			}
			if _, err := root.Lstat("Artist/Album/bait.flac"); !errors.Is(err, errSymlink) {
				t.Errorf("Lstat: %v", err)
			}
			mustNotOpen(t, root, "Artist/Album/bait.flac", errSymlink)
		})
	}
}

// The receipt is read up to 16 MiB, and no further (§4.2).
func TestRootReadReceipt(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)

	data, err := root.ReadReceipt(albumA)
	if want := readFile(t, inLib(dir, albumA+"/"+ReceiptName)); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("ReadReceipt: %d bytes, %v; want the %d bytes of the file", len(data), err, len(want))
	}

	// Exactly the limit: read whole. One byte more: refused.
	atLimit := inLib(dir, albumB+"/"+ReceiptName)
	if err := os.Truncate(atLimit, MaxReceiptBytes); err != nil {
		t.Fatal(err)
	}
	data, err = root.ReadReceipt(albumB)
	if err != nil || len(data) != MaxReceiptBytes {
		t.Fatalf("a receipt of 16 MiB: %d bytes, %v", len(data), err)
	}
	if err := os.Truncate(atLimit, MaxReceiptBytes+1); err != nil {
		t.Fatal(err)
	}
	data, err = root.ReadReceipt(albumB)
	wantCode(t, err, CodeReceiptTooLarge)
	if data != nil {
		t.Fatalf("a receipt beyond the limit still gives %d bytes", len(data))
	}
	if want := "the receipt is longer than 16777216 bytes"; err.Error() != want {
		t.Fatalf("message %q, want %q", err, want)
	}

	// No receipt, and a receipt that is not a regular file.
	if err := os.Remove(inLib(dir, albumC+"/"+ReceiptName)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.ReadReceipt(albumC); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing receipt: %v", err)
	}
	if err := os.Remove(inLib(dir, albumD+"/"+ReceiptName)); err != nil {
		t.Fatal(err)
	}
	mkdir(t, inLib(dir, albumD+"/"+ReceiptName))
	if _, err := root.ReadReceipt(albumD); !errors.Is(err, errNotRegular) {
		t.Errorf("a receipt that is a folder: %v", err)
	}
	rename(t, inLib(dir, albumF+"/"+ReceiptName), inLib(dir, albumF+"/real receipt"))
	symlink(t, "real receipt", inLib(dir, albumF+"/"+ReceiptName))
	if _, err := root.ReadReceipt(albumF); !errors.Is(err, errSymlink) {
		t.Errorf("a receipt that is a symbolic link: %v", err)
	}
	lock(t, inLib(dir, albumE+"/"+ReceiptName))
	if _, err := root.ReadReceipt(albumE); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("a receipt that cannot be read: %v", err)
	}
}

// The error of an access names no absolute path: only the path below the
// MusicLib folder.
func TestRootErrorsHoldNoAbsolutePath(t *testing.T) {
	dir := copyFixture(t)
	symlink(t, "cover.jpg", inLib(dir, albumA+"/link"))
	root := openRoot(t, dir)
	_, errMissing := root.Open(albumA + "/missing.flac")
	_, errLink := root.Open(albumA + "/link")
	_, errArtist := root.Albums("Nobody")
	for _, err := range []error{errMissing, errLink, errArtist} {
		if err == nil || strings.Contains(err.Error(), filepath.Dir(dir)) {
			t.Errorf("error %q", err)
		}
	}
}

// An entry that turns into a symbolic link between the check and the
// opening is not followed: while a file is replaced, again and again, by a
// link to a bait inside the MusicLib folder, every Open gives the bytes of
// the real file or an error, and never the bait.
func TestRootOpenWhileTheFileBecomesALink(t *testing.T) {
	dir := newMusicLib(t)
	const rel = "Artist/Album/01 - Track.flac"
	const real = "the real track"
	target := inLib(dir, rel)
	writeFile(t, target, real)
	writeFile(t, inLib(dir, "Artist/Album/bait"), bait)
	root := openRoot(t, dir)

	stop := make(chan struct{})
	var flipper sync.WaitGroup
	flipper.Add(1)
	go func() {
		defer flipper.Done()
		next := target + ".next"
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			var err error
			if i%2 == 0 {
				err = os.Symlink("bait", next)
			} else {
				err = os.WriteFile(next, []byte(real), 0o644)
			}
			if err == nil {
				// rename replaces the entry in one step: it always exists.
				err = os.Rename(next, target)
			}
			if err != nil {
				t.Errorf("replacing the file: %v", err)
				return
			}
		}
	}()

	opened := 0
	for range 3000 {
		// An error is a refusal, whatever the moment made of it: a link
		// that Lstat saw, an entry replaced since, a link os.Root met.
		f, err := root.Open(rel)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		if err := errors.Join(err, f.Close()); err != nil {
			t.Errorf("reading: %v", err)
			break
		}
		if string(data) != real {
			t.Errorf("Open followed a symbolic link and read %q", data)
			break
		}
		opened++
	}
	close(stop)
	flipper.Wait()
	if opened == 0 {
		t.Error("the real file was never opened: the test proved nothing")
	}
}

// One Root serves many goroutines at once.
func TestRootIsSafeForConcurrentUse(t *testing.T) {
	dir := copyFixture(t)
	root := openRoot(t, dir)
	wantCover := readFile(t, inLib(dir, albumA+"/cover.jpg"))
	wantReceipt := readFile(t, inLib(dir, albumE+"/"+ReceiptName))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				f, err := root.Open(albumA + "/cover.jpg")
				if err != nil {
					t.Errorf("Open: %v", err)
					return
				}
				data, err := io.ReadAll(f)
				if err := errors.Join(err, f.Close()); err != nil || !bytes.Equal(data, wantCover) {
					t.Errorf("reading the cover: %d bytes, %v", len(data), err)
					return
				}
				if data, err := root.ReadReceipt(albumE); err != nil || !bytes.Equal(data, wantReceipt) {
					t.Errorf("ReadReceipt: %d bytes, %v", len(data), err)
					return
				}
				if artists, err := root.Artists(); err != nil || len(artists) != 6 {
					t.Errorf("Artists: %q, %v", artists, err)
					return
				}
				if info, err := root.Lstat(albumB + "/01 - One.mp3"); err != nil || info.Size() != 33591 {
					t.Errorf("Lstat: %v, %v", info, err)
					return
				}
				if ok, err := root.StorePresent(); err != nil || !ok {
					t.Errorf("StorePresent: %v, %v", ok, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
