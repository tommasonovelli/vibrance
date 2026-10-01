package library

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// receiptOf is a receipt that lists the paths, in their order. Classify
// reads only the paths, so the sizes and the SHA-256 tell the files apart.
func receiptOf(paths ...string) Receipt {
	r := Receipt{AlbumID: goldenAlbumID, BuildID: goldenBuildID, AlbumRevision: 1, RenderVersion: "r"}
	for i, p := range paths {
		r.Files = append(r.Files, ReceiptFile{Path: p, Size: int64(i), SHA256: sha256Hex([]byte(p))})
	}
	return r
}

// classes says, for each file of the receipt, what Classify made of it:
// "audio disc=N lyrics=<path>", "cover", "lyrics" or "ignored". It fails
// the test if the classification holds a file the receipt does not list,
// or one file in two places.
func classes(t *testing.T, r Receipt, c Classification) map[string]string {
	t.Helper()
	listed := map[string]ReceiptFile{}
	for _, f := range r.Files {
		listed[f.Path] = f
	}
	out := map[string]string{}
	set := func(f ReceiptFile, class string) {
		if listed[f.Path] != f {
			t.Fatalf("%+v is not a file of the receipt", f)
		}
		if old, ok := out[f.Path]; ok && old != class {
			t.Fatalf("%q is both %q and %q", f.Path, old, class)
		}
		out[f.Path] = class
	}
	for _, a := range c.Audio {
		lyrics := "-"
		if a.Lyrics != nil {
			lyrics = a.Lyrics.Path
			set(*a.Lyrics, "lyrics")
		}
		if _, twice := out[a.Path]; twice {
			t.Fatalf("%q is classified twice", a.Path)
		}
		set(a.ReceiptFile, fmt.Sprintf("audio disc=%d lyrics=%s", a.Disc, lyrics))
	}
	if c.Cover != nil {
		set(*c.Cover, "cover")
	}
	for _, f := range r.Files {
		if _, ok := out[f.Path]; !ok {
			out[f.Path] = "ignored"
		}
	}
	return out
}

func TestClassify(t *testing.T) {
	const (
		nfc = "11 - Caf\u00e9"  // é as one code point
		nfd = "11 - Cafe\u0301" // e and a combining accent
	)
	tests := []struct {
		path string
		want string
	}{
		// Tracks at the root, and their lyrics.
		{"01 - So What.flac", "audio disc=0 lyrics=01 - So What.lrc"},
		{"01 - So What.lrc", "lyrics"},
		{"02 - Freddie Freeloader.mp3", "audio disc=0 lyrics=-"},
		{"03 - Blue in Green.m4a", "audio disc=0 lyrics=-"},
		{"04 - No Track.lrc", "ignored"},
		{"05 - Lyrics On A Disc.flac", "audio disc=0 lyrics=-"},
		// The cover: exactly cover.jpg or cover.png at the root.
		{"cover.jpg", "cover"},
		{"cover.png", "ignored"}, // the receipt already has a cover
		{"cover.jpeg", "ignored"},
		{"cover.JPG", "ignored"},
		{"Cover.jpg", "ignored"},
		{"cover.jpg.bak", "ignored"},
		{"cover", "ignored"},
		{"folder.jpg", "ignored"},
		{"Disc 1/cover.jpg", "ignored"},
		// Extras/: everything is ignored.
		{"Extras/cover.jpg", "ignored"},
		{"Extras/06 - Bonus.flac", "ignored"},
		{"Extras/06 - Bonus.lrc", "ignored"},
		{"Extras/Disc 1/01 - Deep.flac", "ignored"},
		{"Extras/booklet.pdf", "ignored"},
		// One folder "Disc N".
		{"Disc 1/01 - One.flac", "audio disc=1 lyrics=Disc 1/01 - One.lrc"},
		{"Disc 1/01 - One.lrc", "lyrics"},
		{"Disc 1/02 - Two.mp3", "audio disc=1 lyrics=-"},
		{"Disc 2/01 - One.flac", "audio disc=2 lyrics=-"}, // the .lrc of Disc 1 is not its own
		{"Disc 2/05 - Lyrics On A Disc.lrc", "ignored"},   // its track is at the root
		{"Disc 2/02 - Two.lrc", "ignored"},                // its track is in Disc 1
		{"Disc 9/01.m4a", "audio disc=9 lyrics=-"},
		{"Disc 10/01.flac", "audio disc=10 lyrics=-"},
		{"Disc 99/01.flac", "audio disc=99 lyrics=-"},
		{"Disc 100/01.flac", "ignored"},
		{"Disc 0/01.flac", "ignored"},
		{"Disc 01/01.flac", "ignored"},
		{"Disc -1/01.flac", "ignored"},
		{"Disc +1/01.flac", "ignored"},
		{"Disc 1.0/01.flac", "ignored"},
		{"Disc 1a/01.flac", "ignored"},
		{"Disc 1 /01.flac", "ignored"},
		{"Disc  1/01.flac", "ignored"},
		{"Disc/01.flac", "ignored"},
		{"Disc /01.flac", "ignored"},
		{"Disc \u0661/01.flac", "ignored"}, // an Arabic-Indic digit one
		{"Disc 18446744073709551617/01.flac", "ignored"},
		{"disc 1/01.flac", "ignored"},
		{"DISC 1/01.flac", "ignored"},
		{"CD 1/01.flac", "ignored"},
		{"Disc 1/Disc 2/01.flac", "ignored"},
		{"Disc 1/sub/01.flac", "ignored"},
		{"Other/01.flac", "ignored"},
		{"Disc 1.flac", "audio disc=0 lyrics=-"}, // a file, not a folder
		{"Disc 3", "ignored"},
		// Extensions are compared as bytes.
		{"07 - Loud.FLAC", "ignored"},
		{"07 - Loud.Mp3", "ignored"},
		{"07 - Loud.M4A", "ignored"},
		{"08 - Shout.flac", "audio disc=0 lyrics=-"},
		{"08 - Shout.LRC", "ignored"},
		{"09 - Other.wav", "ignored"},
		{"09 - Other.ogg", "ignored"},
		{"09 - Other.opus", "ignored"},
		{"09 - Other.mp4", "ignored"},
		{"09 - Other.flac.bak", "ignored"},
		{"09 - Other.flac ", "ignored"},
		{"09 - None", "ignored"},
		{"flac", "ignored"},
		{"09 - Two.tar.flac", "audio disc=0 lyrics=09 - Two.tar.lrc"},
		{"09 - Two.tar.lrc", "lyrics"},
		{"09 - Two.lrc", "ignored"},
		// Spaces and Unicode.
		{"10 -  Two  Spaces .flac", "audio disc=0 lyrics=10 -  Two  Spaces .lrc"},
		{"10 -  Two  Spaces .lrc", "lyrics"},
		{"10 - Two Spaces.lrc", "ignored"},
		{" 10 - Leading.mp3", "audio disc=0 lyrics=-"},
		{"10 - Ünïcödé 漢字 😀.m4a", "audio disc=0 lyrics=10 - Ünïcödé 漢字 😀.lrc"},
		{"10 - Ünïcödé 漢字 😀.lrc", "lyrics"},
		{"10 - ünïcödé 漢字 😀.lrc", "ignored"}, // another name: the comparison keeps the case
		// The base names are compared in NFC.
		{nfc + ".flac", "audio disc=0 lyrics=" + nfd + ".lrc"},
		{nfd + ".lrc", "lyrics"},
		{"Disc 4/" + nfd + ".mp3", "audio disc=4 lyrics=Disc 4/" + nfc + ".lrc"},
		{"Disc 4/" + nfc + ".lrc", "lyrics"},
		// Two .lrc files with one base name in NFC: the first listed.
		{"14 - Zoë.lrc", "lyrics"},
		{"14 - Zoë.lrc", "ignored"},
		{"14 - Zoë.flac", "audio disc=0 lyrics=14 - Zoë.lrc"},
		// Two tracks with one base name share the .lrc.
		{"12 - Same.flac", "audio disc=0 lyrics=12 - Same.lrc"},
		{"12 - Same.mp3", "audio disc=0 lyrics=12 - Same.lrc"},
		{"12 - Same.lrc", "lyrics"},
		// Paths that are not valid are never audio, cover or lyrics.
		{"../13 - Up.flac", "ignored"},
		{"..", "ignored"},
		{"Disc 1/../13 - Up.flac", "ignored"},
		{"Disc 1/..", "ignored"},
		{"./13 - Here.flac", "ignored"},
		{"./cover.jpg", "ignored"},
		{"/13 - Absolute.flac", "ignored"},
		{"/cover.jpg", "ignored"},
		{"/Disc 1/13.flac", "ignored"},
		{"/etc/passwd", "ignored"},
		{`Disc 1\13 - Backslash.flac`, "ignored"},
		{`13 - Back\slash.flac`, "ignored"},
		{`..\13.flac`, "ignored"},
		{`C:\Music\13.flac`, "ignored"},
		{`Disc 1/13\x.flac`, "ignored"},
		{"Disc 1//13.flac", "ignored"},
		{"13.flac/", "ignored"},
		{"13 - Nul\x00.flac", "ignored"},
		{"13 - Not UTF-8\xff.flac", "ignored"},
		{"", "ignored"},
		{".", "ignored"},
		// Other files of an album folder.
		{ReceiptName, "ignored"},
		{".flac", "audio disc=0 lyrics=-"}, // the rule is the extension
		{"notes.txt", "ignored"},
	}
	var paths []string
	want := map[string]string{}
	for _, tc := range tests {
		if _, twice := want[tc.path]; twice {
			t.Fatalf("%q is twice in the table", tc.path)
		}
		paths = append(paths, tc.path)
		want[tc.path] = tc.want
	}
	r := receiptOf(paths...)
	c, err := Classify(r)
	if err != nil {
		t.Fatal(err)
	}
	got := classes(t, r, c)
	for _, tc := range tests {
		if got[tc.path] != tc.want {
			t.Errorf("%q is %q, want %q", tc.path, got[tc.path], tc.want)
		}
	}
	// The audio files keep the order of the receipt.
	var audio []string
	for _, a := range c.Audio {
		audio = append(audio, a.Path)
	}
	var wantAudio []string
	for _, tc := range tests {
		if strings.HasPrefix(tc.want, "audio") {
			wantAudio = append(wantAudio, tc.path)
		}
	}
	if !reflect.DeepEqual(audio, wantAudio) {
		t.Errorf("audio in the order\n%q\nwant\n%q", audio, wantAudio)
	}
}

// A classified file is the file of the receipt, with its size and SHA-256.
func TestClassifyKeepsTheFiles(t *testing.T) {
	r := goldenReceipt()
	c, err := Classify(r)
	if err != nil {
		t.Fatal(err)
	}
	want := Classification{
		Audio: []AudioFile{{ReceiptFile: r.Files[0], Lyrics: &r.Files[1]}},
		Cover: &r.Files[3],
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("classified %+v, want %+v", c, want)
	}
	// It holds copies: changing the receipt afterwards changes nothing.
	r.Files[0].Size, r.Files[1].Size, r.Files[3].Size = -1, -1, -1
	if c.Audio[0].Size != 1234567 || c.Audio[0].Lyrics.Size != 0 || c.Cover.Size != 20 {
		t.Fatalf("the classification shares memory with the receipt: %+v", c)
	}
}

func TestClassifyCover(t *testing.T) {
	tests := []struct {
		paths []string
		want  string
	}{
		{[]string{"01.flac"}, ""},
		{[]string{"01.flac", "cover.jpg"}, "cover.jpg"},
		{[]string{"01.flac", "cover.png"}, "cover.png"},
		{[]string{"cover.jpg", "cover.png"}, "cover.jpg"}, // the order of a receipt
		{[]string{"Extras/cover.jpg", "Disc 1/cover.png"}, ""},
	}
	for _, tc := range tests {
		c, err := Classify(receiptOf(tc.paths...))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if c.Cover != nil {
			got = c.Cover.Path
		}
		if got != tc.want {
			t.Errorf("%q: the cover is %q, want %q", tc.paths, got, tc.want)
		}
	}
}

func TestClassifyNothing(t *testing.T) {
	c, err := Classify(Receipt{})
	if err != nil || c.Audio != nil || c.Cover != nil {
		t.Fatalf("an empty receipt: %+v, %v", c, err)
	}
}

// More than 20,000 files: receipt_too_large (DESIGN.md §6.3 step 1).
func TestClassifyTooLarge(t *testing.T) {
	paths := make([]string, MaxReceiptFiles+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("Extras/%05d.flac", i)
	}
	paths[0] = "01.flac"
	c, err := Classify(receiptOf(paths[:MaxReceiptFiles]...))
	if err != nil || len(c.Audio) != 1 {
		t.Fatalf("%d files: %d audio, %v", MaxReceiptFiles, len(c.Audio), err)
	}
	c, err = Classify(receiptOf(paths...))
	wantCode(t, err, CodeReceiptTooLarge)
	if !reflect.DeepEqual(c, Classification{}) {
		t.Fatalf("a refused receipt still gives %+v", c)
	}
	if want := "the receipt lists 20001 files, and the maximum is 20000"; err.Error() != want {
		t.Fatalf("message %q, want %q", err, want)
	}
}

// The receipts MusicLib really wrote (testdata/FIXTURE.md).
func TestClassifyFixture(t *testing.T) {
	tests := []struct {
		album string
		want  map[string]string
	}{
		{albumA, map[string]string{
			"01 - First Light.flac": "audio disc=0 lyrics=01 - First Light.lrc",
			"01 - First Light.lrc":  "lyrics",
			"02 - Second Wave.flac": "audio disc=0 lyrics=-",
			"03 - Third_.flac":      "audio disc=0 lyrics=-",
			"Extras/cover.jpg":      "ignored",
			"cover.jpg":             "cover",
		}},
		{albumB, map[string]string{"01 - One.mp3": "audio disc=0 lyrics=-", "02 - Two.mp3": "audio disc=0 lyrics=-"}},
		{albumC, map[string]string{"01 - One.m4a": "audio disc=0 lyrics=-", "02 - Two.m4a": "audio disc=0 lyrics=-"}},
		{albumD, map[string]string{"01 - One.m4a": "audio disc=0 lyrics=-", "02 - Two.m4a": "audio disc=0 lyrics=-"}},
		{albumE, map[string]string{
			"Disc 1/01 - Disc One Track One.flac": "audio disc=1 lyrics=-",
			"Disc 1/02 - Disc One Track Two.flac": "audio disc=1 lyrics=-",
			"Disc 2/01 - Disc Two Track One.flac": "audio disc=2 lyrics=-",
		}},
		{albumF, map[string]string{"01 - Same Audio.flac": "audio disc=0 lyrics=-", "02 - Same Audio Again.flac": "audio disc=0 lyrics=-"}},
	}
	if len(tests) != len(fixtureAlbums) {
		t.Fatalf("%d albums in the table, %d in the fixture", len(tests), len(fixtureAlbums))
	}
	for _, tc := range tests {
		t.Run(tc.album, func(t *testing.T) {
			r, err := ParseReceipt(readFile(t, filepath.Join(fixtureDir, filepath.FromSlash(tc.album), ReceiptName)))
			if err != nil {
				t.Fatal(err)
			}
			c, err := Classify(r)
			if err != nil {
				t.Fatal(err)
			}
			if got := classes(t, r, c); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("classified\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// FuzzClassify: for any list of paths, Classify never panics, makes audio,
// cover and lyrics only of files of the receipt that follow the rules of
// §6.3 step 1, puts no file in two classes, and gives the same result
// twice. The paths are the lines of the input.
func FuzzClassify(f *testing.F) {
	f.Add("01 - So What.flac\n01 - So What.lrc\ncover.jpg\nExtras/cover.jpg")
	f.Add("Disc 1/01.flac\nDisc 1/01.lrc\nDisc 2/01.mp3\nDisc 01/01.m4a\nDisc 100/01.flac")
	f.Add("../x.flac\n/x.flac\nDisc 1\\x.flac\nDisc 1/../x.flac\n.\n\n./cover.jpg")
	f.Add("11 - Caf\u00e9.flac\n11 - Cafe\u0301.lrc\ncover.png\ncover.jpg\n.flac\n.lrc")
	f.Add("x.FLAC\nx.flac \nx\x00.flac\nx\xff.flac\nDisc 1/Disc 2/x.flac")
	f.Fuzz(func(t *testing.T, input string) {
		// No path twice, as in a receipt that was parsed.
		var paths []string
		seen := map[string]bool{}
		for _, p := range strings.Split(input, "\n") {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		r := receiptOf(paths...)
		c, err := Classify(r)
		if err != nil {
			if Code(err) != CodeReceiptTooLarge || len(r.Files) <= MaxReceiptFiles {
				t.Fatalf("%d files: %v", len(r.Files), err)
			}
			return
		}
		classes(t, r, c) // every file is one of the receipt, and in one class
		for _, a := range c.Audio {
			dir, name := "", a.Path
			if i := strings.LastIndex(a.Path, "/"); i >= 0 {
				dir, name = a.Path[:i], a.Path[i+1:]
			}
			if !validRelPath(a.Path) || strings.HasPrefix(a.Path, "Extras/") ||
				(!strings.HasSuffix(name, ".flac") && !strings.HasSuffix(name, ".mp3") && !strings.HasSuffix(name, ".m4a")) {
				t.Fatalf("%q is audio", a.Path)
			}
			wantDir := ""
			if a.Disc != 0 {
				wantDir = fmt.Sprintf("Disc %d", a.Disc)
			}
			if dir != wantDir || a.Disc < 0 || a.Disc > 99 {
				t.Fatalf("%q is audio of disc %d", a.Path, a.Disc)
			}
			if a.Lyrics != nil {
				lyricsDir := ""
				if i := strings.LastIndex(a.Lyrics.Path, "/"); i >= 0 {
					lyricsDir = a.Lyrics.Path[:i]
				}
				if !validRelPath(a.Lyrics.Path) || lyricsDir != dir || !strings.HasSuffix(a.Lyrics.Path, ".lrc") {
					t.Fatalf("%q has the lyrics %q", a.Path, a.Lyrics.Path)
				}
			}
		}
		if c.Cover != nil && c.Cover.Path != "cover.jpg" && c.Cover.Path != "cover.png" {
			t.Fatalf("%q is the cover", c.Cover.Path)
		}
		again, err := Classify(r)
		if err != nil || !reflect.DeepEqual(again, c) {
			t.Fatalf("classified twice: %+v then %+v (%v)", c, again, err)
		}
	})
}
