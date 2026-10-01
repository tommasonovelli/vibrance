package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The tests of this package work on real folders in t.TempDir(), which the
// gate puts on an ext4 volume (DESIGN.md §12.1). They build and change
// those folders with os and filepath, as MusicLib or an operator would; the
// code under test reads them only through a Root.

// fixtureDir is testdata/library-v1, a real library/ written by MusicLib
// 1.1.0 (testdata/FIXTURE.md), from this package's folder.
var fixtureDir = filepath.Join("..", "..", "testdata", "library-v1")

// The albums of the fixture and their album_id (testdata/FIXTURE.md).
const (
	albumA = "Aurora Sines/Alpha_ Light_"
	albumB = "Bravo Tones/Beta MP3"
	albumC = "Charlie Waves/Gamma AAC"
	albumD = "Delta Pulse/Delta ALAC"
	albumE = "Écho Café/Epsilon Discs"
	albumF = "Foxtrot Twins/Phi Same Audio"
)

var fixtureAlbums = map[string]string{
	albumA: "01a0f459-ebe7-73fe-9598-e85475ef5cca",
	albumB: "01a0f459-ebc8-7081-991c-2b1c9331e156",
	albumC: "01a0f459-ebc2-7821-a442-bd56dc181ce3",
	albumD: "01a0f459-ebc1-7b68-9fa3-85c82e63e1b1",
	albumE: "01a0f459-eca5-7127-a52f-ab0f69357431",
	albumF: "01a0f459-ec9d-7dc9-825a-074f90185adc",
}

// newMusicLib makes an empty MusicLib folder, as /musiclib is in a healthy
// installation: the store marker and an empty library/. It returns its
// path. Next to it, in the same temporary folder, a test can put what must
// stay out of reach.
func newMusicLib(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "musiclib")
	mkdir(t, filepath.Join(dir, libraryDir))
	writeFile(t, filepath.Join(dir, storeMarker), "store_id=0192a5f0-0000-7000-8000-000000000001\n")
	return dir
}

// copyFixture makes a MusicLib folder whose library/ is a copy of the
// fixture library, and returns its path.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := newMusicLib(t)
	if err := os.CopyFS(filepath.Join(dir, libraryDir), os.DirFS(fixtureDir)); err != nil {
		t.Fatalf("copying the fixture library: %v", err)
	}
	return dir
}

// openRoot opens the MusicLib folder at dir, and closes it when the test
// ends.
func openRoot(t *testing.T, dir string) *Root {
	t.Helper()
	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return root
}

// inLib is the path on disk of rel, a path relative to library/.
func inLib(dir, rel string) string {
	return filepath.Join(dir, libraryDir, filepath.FromSlash(rel))
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeFile writes a file and the folders it needs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	mkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// lock takes every permission from path until the test ends. It needs a
// user that permissions apply to: the gate never runs as root.
func lock(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("the tests must not run as root: permissions would not apply")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	// Before t.TempDir removes the folder, which it could not otherwise.
	t.Cleanup(func() {
		if err := os.Chmod(path, info.Mode().Perm()); err != nil {
			t.Error(err)
		}
	})
}

// sha256Hex is the SHA-256 of data, computed apart from the code under
// test.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// encodeReceipt writes a receipt in the one form MusicLib writes (DESIGN.md
// §4.2): compact JSON on one line, the keys in their order, no final
// newline, and only `"`, `\` and the control characters escaped.
func encodeReceipt(r Receipt) []byte {
	var b bytes.Buffer
	b.WriteString(`{"schema_version":1,"album_id":`)
	writeJSONString(&b, r.AlbumID)
	b.WriteString(`,"build_id":`)
	writeJSONString(&b, r.BuildID)
	b.WriteString(`,"album_revision":` + strconv.FormatInt(r.AlbumRevision, 10))
	b.WriteString(`,"render_version":`)
	writeJSONString(&b, r.RenderVersion)
	b.WriteString(`,"files":[`)
	for i, f := range r.Files {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"relative_path":`)
		writeJSONString(&b, f.Path)
		b.WriteString(`,"size":` + strconv.FormatInt(f.Size, 10) + `,"sha256":`)
		writeJSONString(&b, f.SHA256)
		b.WriteByte('}')
	}
	b.WriteString("]}")
	return b.Bytes()
}

func writeJSONString(b *bytes.Buffer, s string) {
	const hexDigits = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// albumReceipt is a valid receipt of an album with one track.
func albumReceipt(albumID string, revision int64) Receipt {
	return Receipt{
		AlbumID:       albumID,
		BuildID:       "0192a5f0-ffff-7000-8000-000000000001",
		AlbumRevision: revision,
		RenderVersion: "musiclib-render/3 names/1",
		Files:         []ReceiptFile{{Path: "01 - One.flac", Size: 3, SHA256: sha256Hex([]byte("one"))}},
	}
}

// putAlbum writes the album folder rel of the library at dir with the
// receipt r, and returns the receipt_hash.
func putAlbum(t *testing.T, dir, rel string, r Receipt) string {
	t.Helper()
	data := encodeReceipt(r)
	writeFile(t, filepath.Join(inLib(dir, rel), ReceiptName), string(data))
	return sha256Hex(data)
}

// wantCode fails the test unless err has the code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want the code %q", code)
	}
	if got := Code(err); got != code {
		t.Fatalf("code %q, want %q (error: %v)", got, code, err)
	}
}
