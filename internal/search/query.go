package search

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The limits of a query (DESIGN.md §10.2): the words beyond the eighth and
// the characters of a word beyond the sixty-fourth do not count.
const (
	maxWords      = 8
	maxWordLength = 64
)

// Query is what a user searches for, as the MATCH expression of the
// full-text tables. Parse is the only way to make one that finds anything:
// no text of a user is ever a MATCH expression by itself, where `"`, `*`,
// `-`, `NEAR`, `OR` and `column:` have a meaning (T18).
type Query struct {
	match string
}

// Parse splits q into words and asks for the rows that have every word,
// each as the beginning of one of their words: the search works while the
// user types. A q with no word is the empty query, which finds nothing.
//
// Each word becomes "word"*: a string of FTS5, which is never read as an
// operator or as the name of a column, with the prefix mark outside it.
// Only the quote could end the string, and the quote always separates, so
// no word holds one.
//
// q is split only where unicode61, the tokenizer of the index, is certain
// to split too (see separates). A word cut where the index has one token
// would lose results: what follows the cut is the beginning of no token.
// Which characters unicode61 keeps in a token depends on the Unicode tables
// of SQLite, which no list written here can follow: besides letters and
// numbers they hold the private use characters and every character those
// tables do not know, such as the recent emoji. So everything else stays
// in the word, and FTS5 splits it inside the string exactly as it did for
// the index: a word of several tokens is a phrase, and a word of none (an
// em dash, an accent alone) is an empty phrase, which FTS5 leaves out of
// the other words and which alone finds nothing. No word is dropped here.
//
// q is first put in NFC, the form of every name of the index, which the
// scanner writes through names.Normalize. unicode61 takes away the Latin
// accents only: a kana with its combining voiced mark, or a Greek letter with
// its combining breathing, sent decomposed as some clients do, would be other
// tokens than the composed name. NFC and not NFKC, which would make H₂O the
// H2O that the index does not hold. The bytes that are not UTF-8 stay as
// they are, and separate; the 64 characters of a word are counted after it.
func Parse(q string) Query {
	q = norm.NFC.String(q)
	var b strings.Builder
	words, start := 0, -1
	end := func(at int) {
		if start < 0 {
			return
		}
		word := firstRunes(q[start:at], maxWordLength)
		start = -1
		if words == maxWords {
			return
		}
		if words > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		b.WriteString(word)
		b.WriteString(`"*`)
		words++
	}
	for i := 0; i < len(q); {
		r, size := utf8.DecodeRuneInString(q[i:])
		// A byte that is not UTF-8 separates; U+FFFD itself, sent as such,
		// is a character like any other.
		invalid := r == utf8.RuneError && size == 1
		switch {
		case invalid || separates(r):
			end(i)
		case start < 0:
			start = i
		}
		i += size
	}
	end(len(q))
	return Query{match: b.String()}
}

// separates says whether r ends a word of a query: an ASCII character that
// is not a letter or a digit (unicode61 has a fixed table for ASCII), a
// Unicode space (the categories Z) or a control character (Cc, NUL among
// them). TestFindAcrossEverySeparator checks each of them on the index.
func separates(r rune) bool {
	if r < utf8.RuneSelf {
		return !('0' <= r && r <= '9' || 'A' <= r && r <= 'Z' || 'a' <= r && r <= 'z')
	}
	return unicode.Is(unicode.Z, r) || unicode.Is(unicode.Cc, r)
}

// Empty says that the query has no word: it finds nothing.
func (q Query) Empty() bool {
	return q.match == ""
}

// firstRunes is s cut after its first n characters.
func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
