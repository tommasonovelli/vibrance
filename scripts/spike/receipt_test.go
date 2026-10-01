package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixtureRoot is the committed fixture library, from this package's folder.
var fixtureRoot = filepath.Join("..", "..", filepath.FromSlash(fixtureDir))

const validReceipt = `{"schema_version":1,"album_id":"01a0f459-ebc8-7081-991c-2b1c9331e156","build_id":"01a0f459-ec0d-7a61-898d-1eefa1ed54bc","album_revision":3,"render_version":"r/1","files":[{"relative_path":"01 - A.mp3","size":10,"sha256":"f6a61b76eed87651f03d2f8624f5f50238a0be3558b908ec9ca4b0e683a21dfa"},{"relative_path":"Disc 2/01 - \"Q\".mp3","size":0,"sha256":"a96374149cc0654a8f7f98b8c87010fdc3bf2bcf1d30fcf4e8f3cbb28751ab67"}]}`

func TestParseReceiptAcceptsTheCanonicalForm(t *testing.T) {
	r, err := parseReceipt([]byte(validReceipt))
	if err != nil {
		t.Fatal(err)
	}
	if r.AlbumRevision != 3 || len(r.Files) != 2 || r.Files[1].RelativePath != `Disc 2/01 - "Q".mp3` {
		t.Fatalf("parsed %+v", r)
	}
	if got := canonicalReceipt(r); !bytes.Equal(got, []byte(validReceipt)) {
		t.Fatalf("canonical form:\n got %s\nwant %s", got, validReceipt)
	}
}

// Every deviation from the documented form or values is refused: the spike
// would otherwise confirm H5 on receipts it did not really check.
func TestParseReceiptRefuses(t *testing.T) {
	cases := map[string]string{
		"trailing newline":              validReceipt + "\n",
		"spaces":                        strings.Replace(validReceipt, `"schema_version":1,`, `"schema_version": 1, `, 1),
		"keys out of order":             strings.Replace(validReceipt, `"album_id":"01a0f459-ebc8-7081-991c-2b1c9331e156","build_id":"01a0f459-ec0d-7a61-898d-1eefa1ed54bc"`, `"build_id":"01a0f459-ec0d-7a61-898d-1eefa1ed54bc","album_id":"01a0f459-ebc8-7081-991c-2b1c9331e156"`, 1),
		"unknown key":                   strings.Replace(validReceipt, `"files":`, `"title":"x","files":`, 1),
		"schema 2":                      strings.Replace(validReceipt, `"schema_version":1`, `"schema_version":2`, 1),
		"uppercase uuid":                strings.Replace(validReceipt, "01a0f459-ebc8-7081-991c-2b1c9331e156", "01A0F459-EBC8-7081-991C-2B1C9331E156", 1),
		"revision 0":                    strings.Replace(validReceipt, `"album_revision":3`, `"album_revision":0`, 1),
		"empty render":                  strings.Replace(validReceipt, `"render_version":"r/1"`, `"render_version":""`, 1),
		"dot-dot path":                  strings.Replace(validReceipt, `"01 - A.mp3"`, `"../01 - A.mp3"`, 1),
		"absolute path":                 strings.Replace(validReceipt, `"01 - A.mp3"`, `"/01 - A.mp3"`, 1),
		"backslash":                     strings.Replace(validReceipt, `"01 - A.mp3"`, `"a\\01 - A.mp3"`, 1),
		"unsorted files":                strings.Replace(validReceipt, `"01 - A.mp3"`, `"Z - A.mp3"`, 1),
		"uppercase sha":                 strings.Replace(validReceipt, "f6a61b76", "F6A61B76", 1),
		"data after":                    validReceipt + "{}",
		"escaped slash (not canonical)": strings.Replace(validReceipt, `"r/1"`, `"r\/1"`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if r, err := parseReceipt([]byte(raw)); err == nil {
				t.Fatalf("accepted: %+v", r)
			}
		})
	}
}

// copyFixtureAlbum copies one album folder of the fixture into a temporary
// folder and returns its path and receipt.
func copyFixtureAlbum(t *testing.T, rel string) (string, receipt) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "album")
	if err := copyTree(filepath.Join(fixtureRoot, filepath.FromSlash(rel)), dst); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dst, receiptName))
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseReceipt(raw)
	if err != nil {
		t.Fatal(err)
	}
	return dst, r
}

func TestCheckAlbumFiles(t *testing.T) {
	const album = "Aurora Sines/Alpha_ Light_"
	cases := []struct {
		name   string
		change func(t *testing.T, dir string, r *receipt)
		want   string // a substring of one problem; "" for none
	}{
		{"untouched", func(*testing.T, string, *receipt) {}, ""},
		{"a byte changed", func(t *testing.T, dir string, _ *receipt) {
			p := filepath.Join(dir, "cover.jpg")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			b[len(b)/2] ^= 0xff
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "cover.jpg: sha256"},
		{"truncated", func(t *testing.T, dir string, _ *receipt) {
			if err := os.Truncate(filepath.Join(dir, "cover.jpg"), 10); err != nil {
				t.Fatal(err)
			}
		}, "cover.jpg: size 10"},
		{"missing file", func(t *testing.T, dir string, _ *receipt) {
			if err := os.Remove(filepath.Join(dir, "01 - First Light.lrc")); err != nil {
				t.Fatal(err)
			}
		}, "01 - First Light.lrc: listed, not on disk"},
		{"extra file", func(t *testing.T, dir string, _ *receipt) {
			if err := os.WriteFile(filepath.Join(dir, "Extras", "Thumbs.db"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "Extras/Thumbs.db: on disk, not listed"},
		{"lists itself", func(_ *testing.T, _ string, r *receipt) {
			r.Files = append(r.Files, receiptFile{RelativePath: receiptName})
		}, "the receipt lists itself"},
		{"symbolic link", func(t *testing.T, dir string, _ *receipt) {
			if err := os.Symlink("cover.jpg", filepath.Join(dir, "folder.jpg")); err != nil {
				t.Fatal(err)
			}
		}, "folder.jpg: not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, r := copyFixtureAlbum(t, album)
			tc.change(t, dir, &r)
			problems, err := checkAlbumFiles(dir, r)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems on an untouched copy: %q", problems)
				}
				return
			}
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }) {
				t.Fatalf("problems %q, want one with %q", problems, tc.want)
			}
		})
	}
}

// The committed fixture is complete and intact (DESIGN.md §12.2): six
// albums whose receipts match their files byte for byte (Git must not have
// changed a line ending), under 2 MiB, with the four codecs, the cover only
// in album A, two discs in album E and one .lrc.
func TestFixtureLibraryV1(t *testing.T) {
	dirs, err := scanLibrary(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != len(albumSpecs) {
		t.Fatalf("%d albums, want %d", len(dirs), len(albumSpecs))
	}
	total, _, err := treeSize(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if total >= fixtureLimit {
		t.Fatalf("the fixture is %d bytes, the limit is %d", total, fixtureLimit)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	codecs := map[string]int{}
	covers, lyrics, discs := 0, 0, 0
	for _, d := range dirs {
		dir := filepath.Join(fixtureRoot, filepath.FromSlash(d.Rel))
		problems, err := checkAlbumFiles(dir, d.Receipt)
		if err != nil {
			t.Fatal(err)
		}
		if len(problems) > 0 {
			t.Errorf("%s: %q", d.Rel, problems)
		}
		for _, f := range d.Receipt.Files {
			switch {
			case f.RelativePath == "cover.jpg" || f.RelativePath == "cover.png":
				covers++
			case strings.HasSuffix(f.RelativePath, ".lrc"):
				lyrics++
			case strings.HasPrefix(f.RelativePath, "Disc 2/"):
				discs++
			}
			if !isAudio(f.RelativePath) {
				continue
			}
			p, _, err := probe(ctx, filepath.Join(dir, filepath.FromSlash(f.RelativePath)))
			if err != nil {
				t.Fatal(err)
			}
			a, n := p.audio()
			if n != 1 {
				t.Errorf("%s/%s: %d audio streams", d.Rel, f.RelativePath, n)
			}
			codecs[a.CodecName]++
		}
	}
	want := map[string]int{"flac": 8, "mp3": 2, "aac": 2, "alac": 2}
	for c, n := range want {
		if codecs[c] != n {
			t.Errorf("%d %s tracks, want %d (all: %v)", codecs[c], c, n, codecs)
		}
	}
	if covers != 1 || lyrics != 1 || discs != 1 {
		t.Errorf("covers %d, lyrics %d, tracks on disc 2 %d; want 1 each", covers, lyrics, discs)
	}
}
