package names

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Normalize is the form in which a name read from a tag is stored and
// shown: the text as it is, in NFC and without the white space around it
// (DESIGN.md §5.3). The white space inside it and the case of its letters
// are kept. The result is valid UTF-8, since a byte that is not becomes
// U+FFFD, and normalizing it again changes nothing. An empty result means
// that the tag has no value.
func Normalize(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.TrimFunc(norm.NFC.String(s), unicode.IsSpace)
}

// IdentityKey is the key that tells whether two names are the same artist
// (§5.4): the normalized name with every run of white space inside it
// replaced by one space, and case-folded. "Miles Davis", "miles  davis" and
// "MILES DAVIS" have one key; so have "Straße" and "STRASSE", because the
// folding is the full Unicode one and not a lower-casing.
//
// The key is computed from the name, once. It is not meant to be folded
// again, and it is never shown.
func IdentityKey(name string) string {
	s := strings.Join(strings.Fields(Normalize(name)), " ")
	s = strings.Map(foldCherokee, cases.Fold().String(s))
	// The folding of a composed letter may be a decomposed one: without
	// this, two names that differ only in case could have two keys.
	return norm.NFC.String(s)
}

// foldCherokee corrects the only letters for which cases.Fold of
// golang.org/x/text is not the Unicode case folding.
//
// Unicode folds the lower case Cherokee letters to the upper case ones
// (AB70..ABBF to 13A0..13EF, 13F8..13FD to 13F0..13F5), because the upper
// case ones were encoded first. cases.Fold also maps them the other way, so
// it only swaps the two: "Ꮳ" and "ꮳ" would have two keys, and two artists
// would exist for one name. Applied after the folding, this brings both to
// the upper case letter. MusicLib's internal/names has the same correction.
func foldCherokee(r rune) rune {
	switch {
	case r >= 0xAB70 && r <= 0xABBF:
		return r - 0xAB70 + 0x13A0
	case r >= 0x13F8 && r <= 0x13FD:
		return r - 0x13F8 + 0x13F0
	}
	return r
}

// artistNamespace is the namespace of the artist ids. It is a random UUID
// chosen once: changing it changes the id of every artist.
var artistNamespace = uuid.MustParse("da6142e4-9e07-4450-bf9b-8efe2de871ec")

// ArtistID is the id of the artist with that name: the UUIDv5 of its
// identity key, as lowercase text (§5.4). The same name, however it is
// written, always has the same id, with no database to ask; an artist that
// is renamed is another artist.
func ArtistID(name string) string {
	return uuid.NewSHA1(artistNamespace, []byte(IdentityKey(name))).String()
}

// SortKey is the collation key of s, for the sort_key, title_key and
// artist_key columns (§5.5): comparing two keys byte by byte orders the
// texts as the Unicode collation does, with a run of digits ordered by its
// value, so that "Track 2" comes before "Track 10". The key is computed on
// s in NFC; it is never nil.
//
// Two different texts may have one key (the order ignores what does not
// sort), so a list ordered by a key needs the id as its last criterion. The
// keys depend on the version of golang.org/x/text: they are computed again
// when it changes (T26).
func SortKey(s string) []byte {
	// A Collator keeps the text it is working on: one is not safe for
	// concurrent use, so each call has its own.
	c := collate.New(language.Und, collate.Numeric)
	var buf collate.Buffer
	return bytes.Clone(c.KeyFromString(&buf, norm.NFC.String(s)))
}
