package library

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	goldenAlbumID = "0192a5f0-1c2d-7e3f-8a4b-5c6d7e8f9a0b"
	goldenBuildID = "0192a5f0-ffff-7000-8000-000000000001"
	goldenRender  = "musiclib-render/3 names/1 go1.25.14 ffmpeg/8.1.3-musiclib1 musiclib-tags/4 taglib/2.3.2-musiclib1"
)

var (
	shaA = strings.Repeat("a", 64)
	shaF = strings.Repeat("f", 64)
	sha0 = strings.Repeat("0", 64)
	sha9 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// lineSep is U+2028, which MusicLib writes raw and encoding/json would
// escape.
const lineSep = "\u2028"

// goldenText is a receipt exactly as MusicLib 1.1.0 writes one: its
// TestReceiptGolden pins this form byte for byte.
var goldenText = `{"schema_version":1,"album_id":"` + goldenAlbumID + `","build_id":"` + goldenBuildID + `",` +
	`"album_revision":12,"render_version":"` + goldenRender + `",` +
	`"files":[{"relative_path":"01 - So What.flac","size":1234567,"sha256":"` + shaA + `"},` +
	`{"relative_path":"01 - So What.lrc","size":0,"sha256":"` + sha0 + `"},` +
	`{"relative_path":"Extras/<&> é` + lineSep + `.pdf","size":9,"sha256":"` + shaF + `"},` +
	`{"relative_path":"cover.jpg","size":20,"sha256":"` + sha9 + `"}]}`

func goldenReceipt() Receipt {
	return Receipt{
		AlbumID:       goldenAlbumID,
		BuildID:       goldenBuildID,
		AlbumRevision: 12,
		RenderVersion: goldenRender,
		Files: []ReceiptFile{
			{Path: "01 - So What.flac", Size: 1234567, SHA256: shaA},
			{Path: "01 - So What.lrc", Size: 0, SHA256: sha0},
			{Path: "Extras/<&> é" + lineSep + ".pdf", Size: 9, SHA256: shaF},
			{Path: "cover.jpg", Size: 20, SHA256: sha9},
		},
	}
}

// replaceIn replaces the first old in s, which must be there: a case of a
// table must not silently test the unchanged receipt.
func replaceIn(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("%q is not in the receipt", old)
	}
	return strings.Replace(s, old, new, 1)
}

func TestParseReceiptGolden(t *testing.T) {
	got, err := ParseReceipt([]byte(goldenText))
	if err != nil {
		t.Fatal(err)
	}
	if want := goldenReceipt(); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed\n%+v\nwant\n%+v", got, want)
	}
	// The encoder of the tests writes MusicLib's form.
	if enc := encodeReceipt(got); string(enc) != goldenText {
		t.Fatalf("encoded\n%s\nwant\n%s", enc, goldenText)
	}
}

// The receipt_hash is the SHA-256 of the bytes of the file, whatever they
// are.
func TestReceiptHash(t *testing.T) {
	// printf '' | sha256sum, and printf 'abc' | sha256sum.
	if got, want := ReceiptHash(nil), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"; got != want {
		t.Errorf("hash of no bytes: %s", got)
	}
	if got, want := ReceiptHash([]byte("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("hash of abc: %s", got)
	}
	if ReceiptHash([]byte(goldenText)) == ReceiptHash([]byte(goldenText+"\n")) {
		t.Error("a final newline does not change the hash")
	}
}

// The receipts MusicLib 1.1.0 really wrote: each parses, has the album_id
// of testdata/FIXTURE.md, is in the canonical form, and lists exactly the
// files of its folder, with their sizes and SHA-256.
func TestParseReceiptFixture(t *testing.T) {
	for rel, albumID := range fixtureAlbums {
		t.Run(rel, func(t *testing.T) {
			albumDir := filepath.Join(fixtureDir, filepath.FromSlash(rel))
			data := readFile(t, filepath.Join(albumDir, ReceiptName))
			r, err := ParseReceipt(data)
			if err != nil {
				t.Fatal(err)
			}
			if r.AlbumID != albumID {
				t.Errorf("album_id %s, want %s", r.AlbumID, albumID)
			}
			if r.AlbumRevision != 1 || !validUUID(r.BuildID) || !strings.HasPrefix(r.RenderVersion, "musiclib-render/3 ") {
				t.Errorf("parsed %+v", r)
			}
			if !bytes.Equal(encodeReceipt(r), data) {
				t.Errorf("the parsed receipt does not encode back to the bytes of the file:\n%s", encodeReceipt(r))
			}
			if got, want := ReceiptHash(data), sha256Hex(data); got != want {
				t.Errorf("receipt_hash %s, want %s", got, want)
			}

			var onDisk []ReceiptFile
			err = filepath.WalkDir(albumDir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || d.Name() == ReceiptName {
					return err
				}
				relFile, err := filepath.Rel(albumDir, p)
				if err != nil {
					return err
				}
				content, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				onDisk = append(onDisk, ReceiptFile{Path: filepath.ToSlash(relFile), Size: int64(len(content)), SHA256: sha256Hex(content)})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			slices.SortFunc(onDisk, func(a, b ReceiptFile) int { return strings.Compare(a.Path, b.Path) })
			if !reflect.DeepEqual(r.Files, onDisk) {
				t.Errorf("files of the receipt\n%+v\nfiles of the folder\n%+v", r.Files, onDisk)
			}
		})
	}
}

// What the reading tolerates: everything that keeps the meaning of a
// receipt of version 1 (DESIGN.md §4.2: unknown fields are ignored).
func TestParseReceiptAccepts(t *testing.T) {
	s := goldenText
	golden := goldenReceipt()
	with := func(edit func(r *Receipt)) Receipt {
		r := goldenReceipt()
		edit(&r)
		return r
	}
	oneFile := func(path string) Receipt {
		return with(func(r *Receipt) { r.Files = []ReceiptFile{{Path: path, Size: 9, SHA256: shaF}} })
	}
	oneFileText := func(jsonPath string) string {
		return s[:strings.Index(s, `"files":`)] + `"files":[{"relative_path":"` + jsonPath + `","size":9,"sha256":"` + shaF + `"}]}`
	}
	tests := []struct {
		name  string
		input string
		want  Receipt
	}{
		{"an unknown field at the top", replaceIn(t, s, `"album_revision":12,`, `"album_revision":12,"title":"Kind of Blue",`), golden},
		{"an unknown field before schema_version", replaceIn(t, s, `{"schema_version":1,`, `{"x":null,"schema_version":1,`), golden},
		{"unknown fields of every type", replaceIn(t, s, `"files":`, `"a":{"b":[1,2,{"c":null}]},"d":true,"e":-1.5e3,"f":"g","files":`), golden},
		{"an unknown field twice", replaceIn(t, s, `"files":`, `"x":1,"x":2,"files":`), golden},
		{"an unknown field in a file", replaceIn(t, s, `"size":9,`, `"size":9,"mtime":0,`), golden},
		{"a field that differs only by case is unknown", replaceIn(t, s, `"album_revision":12,`, `"album_revision":12,"Album_Revision":99,"ALBUM_ID":"x",`), golden},
		{"the keys in another order", `{"files":[],"render_version":"r","album_revision":3,"build_id":"` + goldenBuildID + `","album_id":"` + goldenAlbumID + `","schema_version":1}`,
			Receipt{AlbumID: goldenAlbumID, BuildID: goldenBuildID, AlbumRevision: 3, RenderVersion: "r", Files: []ReceiptFile{}}},
		{"the keys of a file in another order", replaceIn(t, s, `{"relative_path":"cover.jpg","size":20,"sha256":"`+sha9+`"}`, `{"sha256":"`+sha9+`","size":20,"relative_path":"cover.jpg"}`), golden},
		{"white space", " \t\r\n" + strings.NewReplacer(`{`, "{\n  ", `,`, " ,\n  ", `:`, " : ", `[`, "[ ", `]`, " ]", `}`, "\n}").Replace(replaceIn(t, s, goldenRender, "r")) + "\n\n",
			with(func(r *Receipt) { r.RenderVersion = "r" })},
		{"a final newline", s + "\n", golden},
		{"no files", s[:strings.Index(s, `"files":`)] + `"files":[]}`, with(func(r *Receipt) { r.Files = []ReceiptFile{} })},
		{"an escaped letter in a key", replaceIn(t, s, `"album_id"`, `"album\u005fid"`), golden},
		{"escaped letters in a path", oneFileText(`cover\u002ejpg`), oneFile("cover.jpg")},
		{"an escaped slash in a path", oneFileText(`Disc 1\/01.flac`), oneFile("Disc 1/01.flac")},
		{"an escaped surrogate pair", oneFileText(`\ud83d\ude00.flac`), oneFile("😀.flac")},
		{"an escaped surrogate pair in upper case", oneFileText(`\uD83D\uDE00.flac`), oneFile("😀.flac")},
		{"an escaped backslash before a u", replaceIn(t, s, goldenRender, `a\\ud800`), with(func(r *Receipt) { r.RenderVersion = `a\ud800` })},
		{"U+FFFD written as it is", oneFileText("01 - \ufffd.flac"), oneFile("01 - \ufffd.flac")},
		{"U+FFFD escaped", oneFileText(`01 - \ufffd.flac`), oneFile("01 - \ufffd.flac")},
		{"control characters in render_version", replaceIn(t, s, goldenRender, `a\"b\\c\u0001\u001f\n`), with(func(r *Receipt) { r.RenderVersion = "a\"b\\c\x01\x1f\n" })},
		{"a path with spaces, Unicode and symbols", oneFileText("Disc 2/07 -  Ünï cödé 漢字 😀 <&>'!.m4a"), oneFile("Disc 2/07 -  Ünï cödé 漢字 😀 <&>'!.m4a")},
		{"a path with a colon and a tilde", oneFileText("C:/~x"), oneFile("C:/~x")},
		{"a path with dots that are not segments", oneFileText("...a/..b/c../.d"), oneFile("...a/..b/c../.d")},
		{"the receipt lists itself", oneFileText(ReceiptName), oneFile(ReceiptName)},
		{"the largest size and revision", replaceIn(t, replaceIn(t, s, `"size":9,`, `"size":9223372036854775807,`), `"album_revision":12`, `"album_revision":9223372036854775807`),
			with(func(r *Receipt) { r.AlbumRevision = math.MaxInt64; r.Files[2].Size = math.MaxInt64 })},
		{"files in byte order, not in UTF-16 order", s[:strings.Index(s, `"files":`)] +
			`"files":[{"relative_path":"B","size":9,"sha256":"` + shaF + `"},{"relative_path":"a","size":9,"sha256":"` + shaF + `"},` +
			`{"relative_path":"\uff21","size":9,"sha256":"` + shaF + `"},{"relative_path":"😀","size":9,"sha256":"` + shaF + `"}]}`,
			with(func(r *Receipt) {
				r.Files = []ReceiptFile{{"B", 9, shaF}, {"a", 9, shaF}, {"\uff21", 9, shaF}, {"😀", 9, shaF}}
			})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReceipt([]byte(tc.input))
			if err != nil {
				t.Fatalf("refused: %v\n%s", err, tc.input)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsed\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// What the reading refuses, and with which code. Each case changes one
// thing of the golden receipt, which is accepted.
func TestParseReceiptRefuses(t *testing.T) {
	s := goldenText
	if _, err := ParseReceipt([]byte(s)); err != nil {
		t.Fatalf("the golden receipt: %v", err)
	}
	re := func(old, new string) string { return replaceIn(t, s, old, new) }
	head := s[:strings.Index(s, `"files":`)]
	files := func(value string) string { return head + `"files":` + value + `}` }
	file := func(path, size, sum string) string {
		return files(`[{"relative_path":` + path + `,"size":` + size + `,"sha256":` + sum + `}]`)
	}
	okSum := `"` + shaF + `"`
	path := func(jsonPath string) string { return file(`"`+jsonPath+`"`, "9", okSum) }
	if _, err := ParseReceipt([]byte(path("x.flac"))); err != nil {
		t.Fatalf("the one-file receipt: %v", err)
	}
	const invalid, unsupported = CodeReceiptInvalid, CodeReceiptSchemaUnsupported

	tests := []struct {
		name  string
		input string
		code  string
	}{
		// Not one JSON object.
		{"empty", "", invalid},
		{"white space only", " \n\t", invalid},
		{"null", "null", invalid},
		{"an array", "[" + s + "]", invalid},
		{"a string", `"` + goldenAlbumID + `"`, invalid},
		{"a number", "1", invalid},
		{"truncated", s[:len(s)/2], invalid},
		{"without the closing brace", s[:len(s)-1], invalid},
		{"two objects", s + s, invalid},
		{"two objects on two lines", s + "\n" + s, invalid},
		{"data after the object", s + "x", invalid},
		{"a closing brace too many", s + "}", invalid},
		{"a byte order mark", "\ufeff" + s, invalid},
		{"a comment", "// receipt\n" + s, invalid},
		{"single quotes", strings.ReplaceAll(s, `"`, `'`), invalid},
		{"a missing comma", re(`1,"album_id"`, `1 "album_id"`), invalid},
		{"a comma too many in files", re(`"}]}`, `"},]}`), invalid},
		{"a comma too many at the top", re(`"}]}`, `"}],}`), invalid},
		{"a name that is not a string", re(`{"schema_version":1,`, `{"schema_version":1,2:3,`), invalid},
		{"an unknown field nested too deep", re(`"files":`, `"x":`+strings.Repeat("[", 10001)+strings.Repeat("]", 10001)+`,"files":`), invalid},
		{"an unknown field that is not JSON", re(`"files":`, `"x":{1},"files":`), invalid},
		// Not UTF-8.
		{"invalid UTF-8 in a path", re("cover.jpg", "cover\xff.jpg"), invalid},
		{"invalid UTF-8 in render_version", re(goldenRender, "r\xc3"), invalid},
		{"invalid UTF-8 in an unknown field", re(`"files":`, "\"x\":\"\xff\",\"files\":"), invalid},
		{"UTF-16", "\xff\xfe{\x00}\x00", invalid},
		// schema_version.
		{"schema_version missing", re(`"schema_version":1,`, ``), invalid},
		{"schema_version null", re(`"schema_version":1`, `"schema_version":null`), invalid},
		{"schema_version as text", re(`"schema_version":1`, `"schema_version":"1"`), invalid},
		{"schema_version 1.0", re(`"schema_version":1`, `"schema_version":1.0`), invalid},
		{"schema_version 1e0", re(`"schema_version":1`, `"schema_version":1e0`), invalid},
		{"schema_version true", re(`"schema_version":1`, `"schema_version":true`), invalid},
		{"schema_version beyond int64", re(`"schema_version":1`, `"schema_version":99999999999999999999`), invalid},
		{"schema_version twice", re(`"schema_version":1,`, `"schema_version":1,"schema_version":1,`), invalid},
		{"schema_version 2 and then 1", re(`"schema_version":1,`, `"schema_version":2,"schema_version":1,`), invalid},
		{"schema_version 1 and, at the end, 2", re(`"}]}`, `"}],"schema_version":2}`), invalid},
		{"schema_version only in another case", re(`"schema_version"`, `"Schema_Version"`), invalid},
		{"schema_version 2", re(`"schema_version":1`, `"schema_version":2`), unsupported},
		{"schema_version 0", re(`"schema_version":1`, `"schema_version":0`), unsupported},
		{"schema_version -1", re(`"schema_version":1`, `"schema_version":-1`), unsupported},
		{"schema_version 2 with another shape", `{"schema_version":2,"album":{"id":7},"files":{"cover.jpg":20}}`, unsupported},
		{"schema_version 2 and nothing else", `{"schema_version":2}`, unsupported},
		{"schema_version 2 at the end", `{"files":"none","schema_version":2}`, unsupported},
		// album_id and build_id.
		{"album_id missing", re(`"album_id":"`+goldenAlbumID+`",`, ``), invalid},
		{"album_id null", re(`"album_id":"`+goldenAlbumID+`"`, `"album_id":null`), invalid},
		{"album_id a number", re(`"album_id":"`+goldenAlbumID+`"`, `"album_id":7`), invalid},
		{"album_id an array", re(`"album_id":"`+goldenAlbumID+`"`, `"album_id":["`+goldenAlbumID+`"]`), invalid},
		{"album_id empty", re(goldenAlbumID, ``), invalid},
		{"album_id not a UUID", re(goldenAlbumID, `kind-of-blue`), invalid},
		{"album_id in upper case", re(goldenAlbumID, strings.ToUpper(goldenAlbumID)), invalid},
		{"album_id as a URN", re(goldenAlbumID, `urn:uuid:`+goldenAlbumID), invalid},
		{"album_id in braces", re(goldenAlbumID, `{`+goldenAlbumID+`}`), invalid},
		{"album_id without hyphens", re(goldenAlbumID, strings.ReplaceAll(goldenAlbumID, "-", "")), invalid},
		{"album_id with a space", re(goldenAlbumID, goldenAlbumID+" "), invalid},
		{"album_id the nil UUID", re(goldenAlbumID, `00000000-0000-0000-0000-000000000000`), invalid},
		{"album_id twice", re(`"build_id"`, `"album_id":"`+goldenAlbumID+`","build_id"`), invalid},
		{"album_id only in another case", re(`"album_id"`, `"ALBUM_ID"`), invalid},
		{"build_id missing", re(`"build_id":"`+goldenBuildID+`",`, ``), invalid},
		{"build_id in upper case", re(goldenBuildID, strings.ToUpper(goldenBuildID)), invalid},
		{"build_id the nil UUID", re(goldenBuildID, `00000000-0000-0000-0000-000000000000`), invalid},
		{"build_id not a UUID", re(goldenBuildID, `x`), invalid},
		// album_revision.
		{"album_revision missing", re(`"album_revision":12,`, ``), invalid},
		{"album_revision 0", re(`"album_revision":12`, `"album_revision":0`), invalid},
		{"album_revision negative", re(`"album_revision":12`, `"album_revision":-12`), invalid},
		{"album_revision 12.0", re(`"album_revision":12`, `"album_revision":12.0`), invalid},
		{"album_revision 12.5", re(`"album_revision":12`, `"album_revision":12.5`), invalid},
		{"album_revision with an exponent", re(`"album_revision":12`, `"album_revision":1.2e1`), invalid},
		{"album_revision as text", re(`"album_revision":12`, `"album_revision":"12"`), invalid},
		{"album_revision null", re(`"album_revision":12`, `"album_revision":null`), invalid},
		{"album_revision beyond int64", re(`"album_revision":12`, `"album_revision":9223372036854775808`), invalid},
		{"album_revision twice", re(`"album_revision":12,`, `"album_revision":12,"album_revision":13,`), invalid},
		// render_version.
		{"render_version missing", re(`"render_version":"`+goldenRender+`",`, ``), invalid},
		{"render_version empty", re(goldenRender, ``), invalid},
		{"render_version a number", re(`"`+goldenRender+`"`, `3`), invalid},
		{"render_version null", re(`"`+goldenRender+`"`, `null`), invalid},
		{"render_version with a lone surrogate", re(goldenRender, `r\ud800`), invalid},
		// files.
		{"files missing", head[:len(head)-1] + `}`, invalid},
		{"files null", files(`null`), invalid},
		{"files an object", files(`{}`), invalid},
		{"files a string", files(`"cover.jpg"`), invalid},
		{"files twice", re(`"files":`, `"files":[],"files":`), invalid},
		{"a file that is a string", files(`["cover.jpg"]`), invalid},
		{"a file that is null", files(`[null]`), invalid},
		{"a file that is an array", files(`[["cover.jpg",20,"` + shaF + `"]]`), invalid},
		{"a file that is empty", files(`[{}]`), invalid},
		{"a file without relative_path", files(`[{"size":9,"sha256":` + okSum + `}]`), invalid},
		{"a file without size", files(`[{"relative_path":"x","sha256":` + okSum + `}]`), invalid},
		{"a file without sha256", files(`[{"relative_path":"x","size":9}]`), invalid},
		{"a file with relative_path twice", files(`[{"relative_path":"x","relative_path":"y","size":9,"sha256":` + okSum + `}]`), invalid},
		{"a file with size twice", files(`[{"relative_path":"x","size":9,"size":9,"sha256":` + okSum + `}]`), invalid},
		{"a file with path instead of relative_path", files(`[{"path":"x","size":9,"sha256":` + okSum + `}]`), invalid},
		{"the second file is not valid", files(`[{"relative_path":"x","size":9,"sha256":` + okSum + `},{"relative_path":"y"}]`), invalid},
		// relative_path (§4.2: no "..", no absolute path, no backslash).
		{"a path that is empty", path(``), invalid},
		{"a path that is a dot", path(`.`), invalid},
		{"a path that is dot-dot", path(`..`), invalid},
		{"a path that climbs", path(`../x.flac`), invalid},
		{"a path that climbs twice", path(`../../etc/passwd`), invalid},
		{"a path with dot-dot inside", path(`Disc 1/../x.flac`), invalid},
		{"a path that ends in dot-dot", path(`Disc 1/..`), invalid},
		{"a path with a dot segment", path(`./x.flac`), invalid},
		{"a path with a dot segment inside", path(`Disc 1/./x.flac`), invalid},
		{"an absolute path", path(`/cover.jpg`), invalid},
		{"an absolute path to another root", path(`/etc/passwd`), invalid},
		{"a path with an empty segment", path(`Disc 1//x.flac`), invalid},
		{"a path that ends with a slash", path(`Disc 1/`), invalid},
		{"a path with a backslash", path(`Disc 1\\x.flac`), invalid},
		{"a Windows path", path(`C:\\Music\\x.flac`), invalid},
		{"a path that climbs with backslashes", path(`..\\x.flac`), invalid},
		{"a path with a NUL", path(`x\u0000.flac`), invalid},
		{"a path with a lone high surrogate", path(`x\ud800.flac`), invalid},
		{"a path with a lone low surrogate", path(`x\udc00.flac`), invalid},
		{"a path with surrogates in the wrong order", path(`x\ude00\ud83d.flac`), invalid},
		{"a path with a high surrogate and a letter", path(`x\ud83d\u0041.flac`), invalid},
		{"a path that is a number", file(`7`, "9", okSum), invalid},
		{"a path that is null", file(`null`, "9", okSum), invalid},
		{"a path that is an array", file(`["x"]`, "9", okSum), invalid},
		// size.
		{"a negative size", file(`"x"`, "-9", okSum), invalid},
		{"a fractional size", file(`"x"`, "9.5", okSum), invalid},
		{"a size with an exponent", file(`"x"`, "1e3", okSum), invalid},
		{"a size as text", file(`"x"`, `"9"`, okSum), invalid},
		{"a size that is null", file(`"x"`, "null", okSum), invalid},
		{"a size beyond int64", file(`"x"`, "9223372036854775808", okSum), invalid},
		{"a size with a leading zero", file(`"x"`, "09", okSum), invalid},
		{"a size that is NaN", file(`"x"`, "NaN", okSum), invalid},
		{"a size of minus zero", file(`"x"`, "-0", okSum), invalid},
		{"schema_version minus zero", re(`"schema_version":1`, `"schema_version":-0`), invalid},
		// sha256.
		{"a sha256 in upper case", file(`"x"`, "9", `"`+strings.ToUpper(shaF)+`"`), invalid},
		{"a sha256 of 63 digits", file(`"x"`, "9", `"`+shaF[1:]+`"`), invalid},
		{"a sha256 of 65 digits", file(`"x"`, "9", `"`+shaF+`f"`), invalid},
		{"a sha256 that is not hex", file(`"x"`, "9", `"`+strings.Repeat("g", 64)+`"`), invalid},
		{"a sha256 with a prefix", file(`"x"`, "9", `"sha256:`+shaF[7:]+`"`), invalid},
		{"a sha256 that is empty", file(`"x"`, "9", `""`), invalid},
		{"a sha256 that is a number", file(`"x"`, "9", `1`), invalid},
		{"a sha256 that is null", file(`"x"`, "9", `null`), invalid},
		// The order of the files.
		{"files not in byte order", re(`"relative_path":"01 - So What.flac"`, `"relative_path":"zz.flac"`), invalid},
		{"a path twice", re(`"relative_path":"01 - So What.lrc"`, `"relative_path":"01 - So What.flac"`), invalid},
		{"files in UTF-16 order", files(`[{"relative_path":"😀","size":9,"sha256":` + okSum + `},{"relative_path":"\uff21","size":9,"sha256":` + okSum + `}]`), invalid},
		{"files in an order that ignores case", files(`[{"relative_path":"a","size":9,"sha256":` + okSum + `},{"relative_path":"B","size":9,"sha256":` + okSum + `}]`), invalid},
	}
	if len(tests) <= 40 {
		t.Fatalf("%d hostile receipts: the table must have more than 40", len(tests))
	}
	seen := map[string]bool{}
	for _, tc := range tests {
		if seen[tc.name] {
			t.Fatalf("the case %q is twice in the table", tc.name)
		}
		seen[tc.name] = true
		t.Run(tc.name, func(t *testing.T) {
			if tc.input == s {
				t.Fatal("the case is the golden receipt, unchanged")
			}
			r, err := ParseReceipt([]byte(tc.input))
			wantCode(t, err, tc.code)
			if !reflect.DeepEqual(r, Receipt{}) {
				t.Errorf("a refused receipt still gives %+v", r)
			}
		})
	}
}

// The message of a refusal says what is wrong, for the list of problems.
func TestParseReceiptMessages(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{replaceIn(t, goldenText, `"schema_version":1`, `"schema_version":2`), "the receipt has schema_version 2, and only 1 is supported"},
		{replaceIn(t, goldenText, `"cover.jpg"`, `"/cover.jpg"`), "the receipt is not valid: files[3]: relative_path is missing or is not a valid relative path"},
		{replaceIn(t, goldenText, `"album_revision":12`, `"album_revision":0`), "the receipt is not valid: album_revision is missing or is not an integer above zero"},
	}
	for _, tc := range tests {
		_, err := ParseReceipt([]byte(tc.input))
		if err == nil || err.Error() != tc.want {
			t.Errorf("message %q, want %q", err, tc.want)
		}
	}
}

// receiptSeeds are the inputs the fuzzing starts from: the real receipts,
// and what the tables accept and refuse.
func receiptSeeds(f *testing.F) [][]byte {
	f.Helper()
	seeds := [][]byte{
		[]byte(goldenText), []byte(""), []byte("{}"), []byte(`{"schema_version":2}`),
		[]byte(`{"schema_version":1,"schema_version":1}`),
		[]byte(strings.Replace(goldenText, "cover.jpg", `x\ud800`, 1)),
		[]byte(strings.Replace(goldenText, "cover.jpg", `\ud83d\ude00`, 1)),
		[]byte(strings.Replace(goldenText, "cover.jpg", `..\/x`, 1)),
		[]byte(strings.Replace(goldenText, `"files":`, `"x":[{"y":null}],"files":`, 1)),
		[]byte(" " + strings.ReplaceAll(goldenText, ",", " ,\n") + "\n"),
	}
	for rel := range fixtureAlbums {
		data, err := os.ReadFile(filepath.Join(fixtureDir, filepath.FromSlash(rel), ReceiptName))
		if err != nil {
			f.Fatal(err)
		}
		seeds = append(seeds, data)
	}
	return seeds
}

// FuzzParseReceipt: the reading of a receipt never panics; what it refuses
// has a code and gives nothing; what it accepts is JSON that the standard
// decoder reads to the same values, obeys every rule of §4.2, and is the
// same receipt once written in MusicLib's form and read again.
func FuzzParseReceipt(f *testing.F) {
	for _, seed := range receiptSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ParseReceipt(data)
		if err != nil {
			if code := Code(err); code != CodeReceiptInvalid && code != CodeReceiptSchemaUnsupported {
				t.Fatalf("refused with the code %q: %v", code, err)
			}
			if !reflect.DeepEqual(r, Receipt{}) {
				t.Fatalf("a refused receipt gives %+v", r)
			}
			return
		}
		if !utf8.Valid(data) || !json.Valid(data) {
			t.Fatal("accepted bytes that are not JSON in UTF-8")
		}
		checkAgainstStandardDecoder(t, data, r)
		if !validUUID(r.AlbumID) || !validUUID(r.BuildID) || r.AlbumRevision <= 0 || r.RenderVersion == "" || r.Files == nil {
			t.Fatalf("accepted %+v", r)
		}
		for i, file := range r.Files {
			if !validRelPath(file.Path) || strings.Contains(file.Path, "\\") || strings.HasPrefix(file.Path, "/") ||
				slices.Contains(strings.Split(file.Path, "/"), "..") {
				t.Fatalf("accepted the path %q", file.Path)
			}
			if file.Size < 0 || !validSHA256(file.SHA256) {
				t.Fatalf("accepted the file %+v", file)
			}
			if i > 0 && r.Files[i-1].Path >= file.Path {
				t.Fatalf("accepted %q after %q", file.Path, r.Files[i-1].Path)
			}
		}
		again, err := ParseReceipt(encodeReceipt(r))
		if err != nil {
			t.Fatalf("the accepted receipt, written again, is refused: %v", err)
		}
		if !reflect.DeepEqual(again, r) {
			t.Fatalf("written and read again\n%+v\nwas\n%+v", again, r)
		}
		if _, err := Classify(r); err != nil && Code(err) != CodeReceiptTooLarge {
			t.Fatalf("Classify: %v", err)
		}
	})
}

// checkAgainstStandardDecoder compares an accepted receipt with what
// encoding/json reads from the same bytes into generic values, where the
// names of the members are compared exactly.
func checkAgainstStandardDecoder(t *testing.T, data []byte, r Receipt) {
	t.Helper()
	var top map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&top); err != nil {
		t.Fatalf("the standard decoder refuses it: %v", err)
	}
	number := func(v any) string {
		n, _ := v.(json.Number)
		return n.String()
	}
	if top["album_id"] != r.AlbumID || top["build_id"] != r.BuildID || top["render_version"] != r.RenderVersion ||
		number(top["schema_version"]) != "1" || number(top["album_revision"]) != strconv.FormatInt(r.AlbumRevision, 10) {
		t.Fatalf("the standard decoder reads %v, the receipt is %+v", top, r)
	}
	files, _ := top["files"].([]any)
	if len(files) != len(r.Files) {
		t.Fatalf("the standard decoder reads %d files, the receipt has %d", len(files), len(r.Files))
	}
	for i, v := range files {
		file, _ := v.(map[string]any)
		if file["relative_path"] != r.Files[i].Path || file["sha256"] != r.Files[i].SHA256 ||
			number(file["size"]) != strconv.FormatInt(r.Files[i].Size, 10) {
			t.Fatalf("the standard decoder reads the file %v, the receipt has %+v", file, r.Files[i])
		}
	}
}
