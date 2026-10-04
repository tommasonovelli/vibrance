package names

import (
	"bytes"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// The texts of these tests write every character outside ASCII as an
// escape: a letter with a mark is one code point or two, and a space is
// one of several, and the source must say which.

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unchanged", "Miles Davis", "Miles Davis"},
		{"the case is kept", "mILES dAVIS", "mILES dAVIS"},
		{"outer spaces", "  Kind of Blue\t", "Kind of Blue"},
		{"outer newline", "\r\nA\n", "A"},
		{"outer no-break and ideographic spaces", "\u00a0A\u3000", "A"},
		{"inner spaces are kept", "So  What", "So  What"},
		{"several values stay one text", "A; B", "A; B"},
		{"nfd becomes nfc", "Bjo\u0308rk", "Bj\u00f6rk"},
		{"nfc stays", "Bj\u00f6rk", "Bj\u00f6rk"},
		{"a mark after an outer space", " \u0301a", "\u0301a"},
		{"empty", "", ""},
		{"only spaces", " \t\u00a0\n", ""},
		{"a zero width space is not a space", "\u200bA", "\u200bA"},
		{"bytes that are not UTF-8", "a\xffb", "a\ufffdb"},
		{"only bytes that are not UTF-8", "\xff\xfe", "\ufffd"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// texts is a generator of texts made of the characters that matter to the
// normalization and to the order: letters in both cases, digits, spaces of
// several kinds, marks that compose, letters whose folding is not their
// lower case, other scripts, and a byte that is not UTF-8.
type texts struct{ rng *rand.Rand }

var textPieces = []string{
	"a", "b", "z", "A", "B", "Z", "e", "E", "o", "O", "s", "S",
	"0", "1", "2", "9", "10", "007",
	" ", " ", "  ", "\t", "\u00a0", "\u3000",
	"\u0301", "\u0308", "\u0323", "\u00e9", "\u00c9", "\u00f6", "\u00d6",
	"\u00df", "\u1e9e", "\ufb01", "\u0130", "\u0131", "\u03a3", "\u03c3", "\u03c2", "\u0390", "\u03aa\u0301",
	"\u13e8", "\uabb8", "\u13f0", "\u13f8",
	"\u6771", "\u4eac", "\u0416", "\u0436", "\u05d0", "-", "(", ")", "&", ".", "'", "\u200b", "\U0001f3b5",
	"\xff",
}

func (g texts) text() string {
	var b strings.Builder
	for n := g.rng.IntN(9); n > 0; n-- {
		b.WriteString(textPieces[g.rng.IntN(len(textPieces))])
	}
	return b.String()
}

// What Normalize promises, on texts of every kind: the result is valid
// UTF-8 in NFC with no white space around it, and normalizing it again
// changes nothing.
func TestNormalizeProperties(t *testing.T) {
	check := func(in string) {
		t.Helper()
		got := Normalize(in)
		if !utf8.ValidString(got) {
			t.Fatalf("Normalize(%q) = %q is not valid UTF-8", in, got)
		}
		if !norm.NFC.IsNormalString(got) {
			t.Fatalf("Normalize(%q) = %q is not in NFC", in, got)
		}
		if got != strings.TrimFunc(got, unicode.IsSpace) {
			t.Fatalf("Normalize(%q) = %q has white space around it", in, got)
		}
		if again := Normalize(got); again != got {
			t.Fatalf("Normalize(%q) = %q, and normalized again %q", in, got, again)
		}
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		check(string(r))
		check(" " + string(r) + " ")
	}
	g := texts{rand.New(rand.NewPCG(1, 1))}
	for range 20000 {
		check(g.text())
	}
}

func TestIdentityKey(t *testing.T) {
	// Each group is one artist: all its names have the key.
	same := []struct {
		key   string
		names []string
	}{
		{"miles davis", []string{"Miles Davis", "miles davis", "MILES DAVIS", "  Miles   Davis ", "Miles\tDavis",
			"Miles\u00a0Davis", "Miles \u3000 Davis\n"}},
		// Bjork with a diaeresis, as one code point and as two.
		{"bj\u00f6rk", []string{"Bj\u00f6rk", "Bjo\u0308rk", "BJ\u00d6RK", "BJO\u0308RK"}},
		// The full folding: a sharp s, small and capital, is "ss".
		{"strasse", []string{"Stra\u00dfe", "STRASSE", "strasse", "STRA\u1e9eE"}},
		// Sisyphos in Greek: the final sigma folds to the plain one.
		{"\u03c3\u03af\u03c3\u03c5\u03c6\u03bf\u03c3", []string{
			"\u03a3\u03af\u03c3\u03c5\u03c6\u03bf\u03c2", "\u03a3\u038a\u03a3\u03a5\u03a6\u039f\u03a3"}},
		// The folding of U+0390 is three code points, which NFC composes
		// again: the same letter written in upper case must end there too.
		{"\u0390", []string{"\u0390", "\u03aa\u0301", "\u03b9\u0308\u0301", "\u0399\u0308\u0301"}},
		// Cherokee: the letter U+13E8 and its lower case, U+ABB8.
		{"\u13e8sa", []string{"\u13e8sa", "\uabb8sa", "\u13e8SA", "\uabb8Sa"}},
		{"ac/dc", []string{"AC/DC", "ac/dc"}},
		{"various artists", []string{"Various Artists", "VARIOUS  ARTISTS"}},
		// Two words in kanji, with a plain and with an ideographic space.
		{"\u6771\u4eac \u4e8b\u5909", []string{"\u6771\u4eac \u4e8b\u5909", "\u6771\u4eac\u3000\u4e8b\u5909"}},
		{"", []string{"", " ", "\t\n"}},
	}
	for _, group := range same {
		for _, name := range group.names {
			if got := IdentityKey(name); got != group.key {
				t.Errorf("IdentityKey(%q) = %q, want %q", name, got, group.key)
			}
		}
	}

	// Names that are not the same artist.
	different := []string{
		"Miles Davis", "MilesDavis", "Miles Davies", "Miles Davis Quintet", "Miles-Davis",
		"The Beatles", "Beatles", "Bjork", "Bj\u00f6rk", "AC/DC", "AC DC", "ACDC",
		"A feat. B", "A", "B", "1", "01", "\u13e8", "\u13e9",
	}
	seen := map[string]string{}
	for _, name := range different {
		key := IdentityKey(name)
		if other, ok := seen[key]; ok {
			t.Errorf("%q and %q have one key, %q", other, name, key)
		}
		seen[key] = name
	}
}

// Every letter has one key, whatever its case: the property the artist ids
// rest on, checked on every code point. It fails without the correction of
// the Cherokee letters. The key of a key is the key, so a single letter has
// one form.
func TestIdentityKeyOfEveryCodePoint(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates are not text
		}
		// A letter between two others, so that a space is inner and a mark
		// has a base.
		name := "a" + string(r) + "a"
		key := IdentityKey(name)
		if again := IdentityKey(key); again != key {
			t.Fatalf("U+%04X: the key of %q is %q, and its key is %q", r, name, key, again)
		}
		// unicode.SimpleFold walks the letters that differ from r only in
		// case.
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if other := IdentityKey("a" + string(f) + "a"); other != key {
				t.Fatalf("U+%04X and U+%04X differ only in case, and have the keys %q and %q", r, f, key, other)
			}
		}
	}
}

func TestIdentityKeyProperties(t *testing.T) {
	g := texts{rand.New(rand.NewPCG(2, 2))}
	for range 20000 {
		name := g.text()
		key := IdentityKey(name)
		if !utf8.ValidString(key) || !norm.NFC.IsNormalString(key) {
			t.Fatalf("IdentityKey(%q) = %q is not valid UTF-8 in NFC", name, key)
		}
		if strings.Join(strings.Fields(key), " ") != key {
			t.Fatalf("IdentityKey(%q) = %q has white space that is not one inner space", name, key)
		}
		// The white space around the name and the form of the text do not
		// change the artist.
		for _, variant := range []string{" " + name + "\t", norm.NFD.String(Normalize(name)), Normalize(name)} {
			if got := IdentityKey(variant); got != key {
				t.Fatalf("IdentityKey(%q) = %q, and of its variant %q it is %q", name, key, variant, got)
			}
		}
	}
}

func TestArtistID(t *testing.T) {
	// The ids are in the database and in the URLs of the clients: a change
	// of the namespace, or of the key of a plain name, must be noticed.
	golden := map[string]string{
		"Miles Davis":     "22d009a4-ca62-5c2c-b442-6ebbad603fbe",
		"Various Artists": "af5fc356-e3c6-50c8-a70e-57b786b86c45",
		"Unknown Artist":  "3246a656-a631-59e5-83a5-a2b9c2b7d0f8",
		"Bj\u00f6rk":      "9ca677ea-1470-504d-ab5d-b3ce173e44aa",
	}
	for name, want := range golden {
		if got := ArtistID(name); got != want {
			t.Errorf("ArtistID(%q) = %s, want %s", name, got, want)
		}
	}

	for _, name := range []string{"Miles Davis", "", "\u6771\u4eac", "AC/DC"} {
		got := ArtistID(name)
		id, err := uuid.Parse(got)
		if err != nil || id.String() != got || len(got) != 36 {
			t.Fatalf("ArtistID(%q) = %q is not a lowercase UUID of 36 characters", name, got)
		}
		if id.Version() != 5 || id.Variant() != uuid.RFC4122 {
			t.Errorf("ArtistID(%q) = %s is version %d, want a UUIDv5", name, got, id.Version())
		}
		if want := uuid.NewSHA1(artistNamespace, []byte(IdentityKey(name))).String(); got != want {
			t.Errorf("ArtistID(%q) = %s, want the UUIDv5 of the identity key, %s", name, got, want)
		}
	}

	// One artist however the name is written, another one for another name.
	if a, b := ArtistID("Miles Davis"), ArtistID(" miles  DAVIS "); a != b {
		t.Errorf("one name written in two ways has the ids %s and %s", a, b)
	}
	if a, b := ArtistID("Bj\u00f6rk"), ArtistID("BJO\u0308RK"); a != b {
		t.Errorf("one name written in two ways has the ids %s and %s", a, b)
	}
	if a, b := ArtistID("\u13e8sa"), ArtistID("\uabb8sa"); a != b {
		t.Errorf("a Cherokee name in its two cases has the ids %s and %s", a, b)
	}
	if a, b := ArtistID("Miles Davis"), ArtistID("Miles Davis Quintet"); a == b {
		t.Errorf("two names have one id, %s", a)
	}
}

func TestSortKeyOrder(t *testing.T) {
	// Each list is in the order its keys must give.
	orders := [][]string{
		// A run of digits is ordered by its value.
		{"Track 1", "Track 2", "Track 9", "Track 10", "Track 11", "Track 100"},
		{"1", "2", "10", "20", "100"},
		{"Vol. 2 Part 10", "Vol. 10 Part 2"},
		// Letters before their case and their accents, unlike the bytes.
		{"apple", "Banana", "cherry", "Date"},
		{"Bjork", "Bj\u00f6rk", "Bjp"},
		{"a", "A", "b", "B"},
		{"Miles Davis", "Miles Davis Quintet", "Mingus"},
		// Articles are not ignored (§5.5).
		{"Smiths", "The Beatles", "Who"},
	}
	for _, order := range orders {
		for i := 1; i < len(order); i++ {
			if bytes.Compare(SortKey(order[i-1]), SortKey(order[i])) >= 0 {
				t.Errorf("the key of %q is not before the key of %q", order[i-1], order[i])
			}
		}
	}
}

func TestSortKey(t *testing.T) {
	if key := SortKey(""); key == nil {
		t.Error("the key of the empty text is nil: the column is NOT NULL")
	}
	// The key is of the text in NFC.
	if a, b := SortKey("Bj\u00f6rk"), SortKey("Bjo\u0308rk"); !bytes.Equal(a, b) {
		t.Errorf("one text in NFC and in NFD has the keys %x and %x", a, b)
	}
	// The collator alone gives the two forms of that text one key, but not
	// those of a letter newer than its tables: the Grantha vowel sign OO,
	// as one code point and as its two parts.
	if a, b := SortKey("a\U0001134bb"), SortKey("a\U00011347\U0001133eb"); !bytes.Equal(a, b) {
		t.Errorf("one text in NFC and in NFD has the keys %x and %x", a, b)
	}
	// Each call returns memory of its own, and no more than the key needs.
	a, b := SortKey("Kind of Blue"), SortKey("Kind of Blue")
	want := bytes.Clone(a)
	for i := range b {
		b[i] = 0
	}
	if !bytes.Equal(a, want) {
		t.Error("two keys share their memory")
	}
	if cap(a) > 2*len(a)+64 {
		t.Errorf("a key of %d bytes keeps %d bytes of memory", len(a), cap(a))
	}
}

// Comparing two keys byte by byte gives the order of the collation
// (language.Und, numeric) of the two texts in NFC: the keys can be compared
// by SQLite with memcmp (§5.5).
func TestSortKeyOrdersAsTheCollator(t *testing.T) {
	c := collate.New(language.Und, collate.Numeric)
	g := texts{rand.New(rand.NewPCG(3, 3))}
	differing := 0
	for range 20000 {
		a, b := g.text(), g.text()
		want := c.CompareString(norm.NFC.String(a), norm.NFC.String(b))
		if got := bytes.Compare(SortKey(a), SortKey(b)); got != want {
			t.Fatalf("the keys of %q and %q compare as %d, and the collator says %d", a, b, got, want)
		}
		if want != 0 {
			differing++
		}
	}
	if differing < 10000 {
		t.Fatalf("only %d pairs of texts differ: the test compares nothing", differing)
	}
}

// SortKey and ArtistID are called by the workers of the scanner and by the
// requests at once.
func TestKeysConcurrently(t *testing.T) {
	g := texts{rand.New(rand.NewPCG(4, 4))}
	inputs := make([]string, 200)
	keys := make([][]byte, len(inputs))
	ids := make([]string, len(inputs))
	for i := range inputs {
		inputs[i] = "Track " + g.text()
		keys[i] = SortKey(inputs[i])
		ids[i] = ArtistID(inputs[i])
	}
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range 5 * len(inputs) {
				i := (n + w*31) % len(inputs)
				if got := SortKey(inputs[i]); !bytes.Equal(got, keys[i]) {
					t.Errorf("the key of %q computed among other goroutines is %x, want %x", inputs[i], got, keys[i])
					return
				}
				if got := ArtistID(inputs[i]); got != ids[i] {
					t.Errorf("the id of %q computed among other goroutines is %s, want %s", inputs[i], got, ids[i])
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A list of names sorted by their keys, as a query would return it.
func TestSortKeySortsAList(t *testing.T) {
	names := []string{"Track 10", "Track 2", "apple", "Banana", "Bj\u00f6rk", "\u6771\u4eac", "", "10", "9"}
	sorted := slices.Clone(names)
	slices.SortFunc(sorted, func(a, b string) int { return bytes.Compare(SortKey(a), SortKey(b)) })
	want := []string{"", "9", "10", "apple", "Banana", "Bj\u00f6rk", "Track 2", "Track 10", "\u6771\u4eac"}
	if !slices.Equal(sorted, want) {
		t.Errorf("sorted by key: %q, want %q", sorted, want)
	}
}

// CollateVersion is the version of golang.org/x/text that go.mod requires:
// a change of the module that is not written here would leave the sort keys
// of an existing database as the old module computed them (T26).
func TestCollateVersionIsTheOneOfGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "golang.org/x/text" {
			found++
			if got := fields[0] + " " + fields[1]; got != CollateVersion {
				t.Fatalf("go.mod requires %q, and CollateVersion is %q", got, CollateVersion)
			}
		}
	}
	if found != 1 {
		t.Fatalf("go.mod names golang.org/x/text %d times, want once", found)
	}
}
