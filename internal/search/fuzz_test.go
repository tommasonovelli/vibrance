package search

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"vibrance/internal/store"
)

// FuzzSearchQuery gives Parse any text and runs what it makes on a real
// index (DESIGN.md §12.5, T18). Whatever the text:
//
//   - nothing panics and SQLite reports no error, so no text is a syntax
//     error of MATCH;
//   - the expression is nothing but strings with the prefix mark, at most
//     8 of at most 64 characters, and no string holds a quote, another
//     ASCII character that is no letter and no digit, a space or a control:
//     there is no place where an operator, a column or a quote of the text
//     could be;
//   - the words of the expression are those of the text, in order;
//   - a text without words finds nothing.
func FuzzSearchQuery(f *testing.F) {
	const acute = string(rune(0x301))
	for _, seed := range []string{
		"", " ", "mil dav", "beyoncé", "Beyoncé", "AC/DC", "坂本龍一", "千のナイフ",
		`"`, `""`, `"a`, `a"`, `"a" "b"`, `a""b`, "*", "a*", "a *", "*a", "-", "-a", "a -b", "a - b", "+a", "^a",
		"NEAR(", "NEAR(a b)", "NEAR(a b, 5)", "a NEAR b", "a OR b", "a AND b", "a NOT b", "OR", "AND", "NOT", "NEAR",
		"title:x", "title : x", "{title artist}:x", "-title:x", "name:x", "rowid:1", "rank", "bm25",
		"(", ")", "(a OR b) AND c", "a\x00b", "\xff\xfe", "á", "́", "‍", "ǅ", "ß", "İ", "ﬁ", "𝔘𝔫𝔦", "𞤀𞤁", "٣",
		"'; DROP TABLE search_tracks; --", `\"`, `a\`, "a\tb\nc", strings.Repeat("a", 100), strings.Repeat("a ", 50),
		strings.Repeat("é", 65), strings.Repeat(`"`, 100),
		"H₂O", "x²", "½", "Ⅷ", "E" + acute + "cho", "Vie" + string(rune(0x323)) + string(rune(0x302)) + "t",
		"a" + acute, "Beyonce" + acute, acute + "a", "a " + acute + " b", strings.Repeat(acute, 64) + "a", strings.Repeat("e"+acute, 40), "कि", "ไม้",
		"A🤝B", "🫶Love", "🫶", "Disco🪩Ball", "Old💔Heart", "A\ue000B", "Apple\uf8ffMusic", "₺ ₽ ₿", "Sigur—Rós", "«miles»", "miles、davis",
		"don’t", "miles — davis", "… miles", "—", "— …", "a\u00a0b", "a\u2028b\u2029c", "a\u0085b", "a\u200bb", "a\ufffdb", "a\xe2\x82b", "\u2e3c",
	} {
		f.Add(seed)
	}
	s := newStore(f)
	index(f, s,
		entry{"Miles Davis", "Kind of Blue", list("So What", "Blue in Green")},
		entry{"Beyoncé", "Lemonade", list("Formation")},
		entry{"Near", "A or B", list("Not", "And", "Title")},
		entry{"坂本龍一", "千のナイフ", list("東風")},
		entry{"Écho Café", "H₂O", list("x²", "½", "Ⅷ")},
		entry{"A🤝B", "🫶Love", list("Disco🪩Ball", "Old💔Heart", "Don’t Stop")},
		entry{"A\ue000B", "Apple\uf8ffMusic", list("Sigur—Rós")},
	)
	f.Fuzz(func(t *testing.T, text string) {
		q := Parse(text)
		want := wordsOf(text)
		checkExpression(t, text, q.match, want)
		var res Results
		err := s.Read(t.Context(), func(tx *store.Queries) (err error) {
			res, err = Find(t.Context(), tx, "", q, everything, 50)
			return err
		})
		if err != nil {
			t.Fatalf("searching %q (%s): %v", text, q.match, err)
		}
		if len(want) == 0 && (len(res.Artists) != 0 || len(res.Albums) != 0 || len(res.Tracks) != 0) {
			t.Fatalf("%q has no word and finds %+v", text, res)
		}
	})
}

// asciiWord are the only ASCII characters of a word of §10.2.
const asciiWord = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ends says whether r ends a word of §10.2. It is the rule of Parse written
// a second time on purpose, in another way: the oracle must not share the
// code it checks.
func ends(r rune) bool {
	if r <= 0x7f {
		return !strings.ContainsRune(asciiWord, r)
	}
	return unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp) || unicode.IsControl(r)
}

// wordsOf is the rule of §10.2 written again, one character at a time: the
// oracle of the fuzz target. A word is a run of characters that do not end
// one, cut to 64 characters; eight count. A byte that is not UTF-8 ends a
// word: ranging over the text gives U+FFFD for it, as for a real U+FFFD,
// which does not, so the bytes tell them apart.
func wordsOf(text string) []string {
	var words []string
	var word []rune
	end := func() {
		if len(word) > 0 && len(words) < 8 {
			words = append(words, string(word[:min(len(word), 64)]))
		}
		word = nil
	}
	for i, r := range text {
		if ends(r) || r == utf8.RuneError && !strings.HasPrefix(text[i:], "�") {
			end()
			continue
		}
		word = append(word, r)
	}
	end()
	return words
}

// checkExpression fails unless match is exactly `"w1"* "w2"* ...` for the
// words wanted, none of which holds a character that ends a word: the
// quote is one of them.
func checkExpression(t *testing.T, text, match string, want []string) {
	t.Helper()
	if !utf8.ValidString(match) {
		t.Fatalf("Parse(%q) is not UTF-8: %q", text, match)
	}
	if len(want) == 0 {
		if match != "" {
			t.Fatalf("Parse(%q) = %s, want nothing", text, match)
		}
		return
	}
	// No word holds a space, so the spaces of the expression are those
	// between its terms.
	terms := strings.Split(match, " ")
	if len(terms) != len(want) {
		t.Fatalf("Parse(%q) = %s: %d terms, want %d", text, match, len(terms), len(want))
	}
	for i, term := range terms {
		word, opened := strings.CutPrefix(term, `"`)
		word, closed := strings.CutSuffix(word, `"*`)
		if !opened || !closed || word != want[i] || word == "" || utf8.RuneCountInString(word) > 64 {
			t.Fatalf("Parse(%q) = %s: term %d is %s, want %q", text, match, i, term, want[i])
		}
		if strings.ContainsFunc(word, ends) || strings.ContainsRune(word, '"') {
			t.Fatalf("Parse(%q) = %s: term %d holds a character that ends a word", text, match, i)
		}
	}
}
