package library

import (
	"fmt"
	"path"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// MaxReceiptFiles is the largest number of files of a receipt that is
// classified (DESIGN.md §6.3 step 1).
const MaxReceiptFiles = 20000

// maxDisc is the highest disc number, of MusicLib and of tracks.disc
// (§5.2).
const maxDisc = 99

// Classification is what the files of a receipt are to the index (§6.3
// step 1). A file that is in none of its fields is ignored.
type Classification struct {
	// Audio are the track files, in the order of the receipt.
	Audio []AudioFile
	// Cover is the cover of the album, nil when it has none.
	Cover *ReceiptFile
}

// AudioFile is one track file.
type AudioFile struct {
	ReceiptFile
	// Disc is the N of the folder "Disc N" the file is in, and 0 for a file
	// at the root of the album.
	Disc int
	// Lyrics is the .lrc file next to the track, nil when it has none.
	Lyrics *ReceiptFile
}

// lyricsKey names the track a .lrc file belongs to: its folder and its
// base name in NFC.
type lyricsKey struct {
	disc int
	stem string
}

// Classify sorts the files of a receipt into audio, cover and lyrics, and
// ignores the rest. It reads only the paths: no disk, no tags. The rules
// compare bytes, so an extension in upper case is not the one MusicLib
// writes, and "cover.JPG" is not the cover.
//
//   - audio: a file with the extension .flac, .mp3 or .m4a, at the root of
//     the album or directly in a folder "Disc N", N from 1 to 99 written
//     without a leading zero. So nothing below Extras/ is audio;
//   - cover: exactly "cover.jpg" or "cover.png" at the root (the first
//     listed, if a receipt has both);
//   - lyrics: a .lrc file whose base name is, compared in NFC, the base
//     name of an audio file of the same folder (the first listed, if two
//     .lrc files have that name).
//
// A path that validRelPath refuses is ignored like any other file: it
// cannot come from ParseReceipt. A receipt with more than MaxReceiptFiles
// files is refused with CodeReceiptTooLarge.
func Classify(r Receipt) (Classification, error) {
	if len(r.Files) > MaxReceiptFiles {
		return Classification{}, &Error{Code: CodeReceiptTooLarge,
			Msg: fmt.Sprintf("the receipt lists %d files, and the maximum is %d", len(r.Files), MaxReceiptFiles)}
	}
	var c Classification
	lyrics := map[lyricsKey]ReceiptFile{}
	for _, f := range r.Files {
		if !validRelPath(f.Path) {
			continue
		}
		if f.Path == "cover.jpg" || f.Path == "cover.png" {
			if c.Cover == nil {
				c.Cover = &f
			}
			continue
		}
		disc, name, ok := trackPlace(f.Path)
		if !ok {
			continue
		}
		switch path.Ext(name) {
		case ".flac", ".mp3", ".m4a":
			c.Audio = append(c.Audio, AudioFile{ReceiptFile: f, Disc: disc})
		case ".lrc":
			key := lyricsKey{disc: disc, stem: stem(name)}
			if _, taken := lyrics[key]; !taken {
				lyrics[key] = f
			}
		}
	}
	for i := range c.Audio {
		key := lyricsKey{disc: c.Audio[i].Disc, stem: stem(path.Base(c.Audio[i].Path))}
		if l, ok := lyrics[key]; ok {
			c.Audio[i].Lyrics = &l
		}
	}
	return c, nil
}

// trackPlace tells whether p is where a track and its lyrics can be: at the
// root of the album (disc 0) or directly in a folder "Disc N". name is the
// name of the file.
func trackPlace(p string) (disc int, name string, ok bool) {
	dir, name, nested := strings.Cut(p, "/")
	if !nested {
		return 0, p, true
	}
	if strings.Contains(name, "/") {
		return 0, "", false
	}
	disc, ok = discNumber(dir)
	return disc, name, ok
}

// discNumber reads the N of a folder named "Disc N" as MusicLib writes it:
// decimal digits with no sign and no leading zero, from 1 to maxDisc.
func discNumber(dir string) (int, bool) {
	digits, ok := strings.CutPrefix(dir, "Disc ")
	if !ok || digits == "" || digits[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
		if n = n*10 + int(digits[i]-'0'); n > maxDisc {
			return 0, false
		}
	}
	return n, true
}

// stem is the base name of a file name, without its extension, in NFC: a
// name is the same whether an accented letter is one code point or two.
func stem(name string) string {
	return norm.NFC.String(strings.TrimSuffix(name, path.Ext(name)))
}
