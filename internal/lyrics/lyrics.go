package lyrics

import (
	"bytes"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxLines is the most lines Parse returns: what a file gives beyond them
// is cut.
const maxLines = 10000

// Lyrics is the text of a track. A file with at least one time tag is
// synced: every line has a time and the lines are in the order of their
// times. A file without any is not: no line has a time and the lines are in
// the order of the file; an empty line stands for the empty lines between
// two stanzas, so it is never the first, the last or next to another.
type Lyrics struct {
	Synced bool
	Lines  []Line // never nil
}

// Line is one line of lyrics. TimeMS is the moment of the track at which
// the line begins, in milliseconds and never negative; it is nil in lyrics
// that are not synced. Text is valid UTF-8 without a line ending and
// without white space around it. It may be empty: an instrumental pause in
// synced lyrics, the space between two stanzas in the others.
type Line struct {
	TimeMS *int64
	Text   string
}

// The header tags, which are never text: all are ignored, except the
// offset, which moves every time of the file. The list is closed: any other
// [letters:...] is text, as the section headers of plain lyrics are
// ([Chorus: Someone]).
var identKeys = []string{
	"ar", "ti", "al", "by", "length", "offset",
	"au", "lr", "re", "ve", "tool", "la", "id",
}

const offsetKey = "offset"

// timedLine is a line of the file under one of its time tags, before the
// offset of the file is applied.
type timedLine struct {
	ms   int64
	text string
}

// Parse reads an LRC file. It accepts any bytes and never fails: what is
// not a tag is text.
//
//   - A UTF-8 byte order mark at the start is dropped, and bytes that are
//     not UTF-8 become U+FFFD. A line ends with "\n", "\r\n" or "\r".
//   - A line may begin with any number of tags, with white space around
//     them. A time tag is [mm:ss], [mm:ss.x], [mm:ss.xx], [mm:ss.xxx], the
//     same with a colon before the fraction, [mm:ss:xx], or the same after
//     hours, [hh:mm:ss.xx]: the first number has any number of digits, the
//     others two and are at most 59, the fraction one to three. Three
//     numbers without a point are never hours: [01:02:03] is 1 minute, 2
//     seconds and 3 hundredths. The line is returned once for each of its
//     time tags.
//   - The header tags, [ar:], [ti:], [al:], [by:], [length:], [au:], [lr:],
//     [re:], [ve:], [tool:], [la:] and [id:], are ignored. [offset:n], a
//     number of milliseconds with an optional sign, is subtracted from
//     every time of the file, wherever the tag is; a time never goes below
//     zero. The last offset of the file is the one that counts.
//   - Any other bracket begins the text. Word time tags, <mm:ss.xx>, are
//     removed from the text.
//   - In a file with at least one time tag the lines without one are
//     dropped, the lines with an empty text are kept, and the lines are
//     put in the order of their time tags, those of one time in the order
//     of the file.
//   - In a file without time tags the empty lines between two lines of
//     text are kept as one empty line, and those before the first and
//     after the last are dropped. A line that had only header tags is not
//     an empty line: it is no line.
//   - Lines beyond the first 10,000 are cut, after the ordering; the empty
//     ones count.
func Parse(data []byte) Lyrics {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	rest := strings.ToValidUTF8(string(data), string(utf8.RuneError))

	var (
		timed  []timedLine
		plain  = []Line{}
		offset int64
	)
	for rest != "" {
		var line string
		line, rest = cutLine(rest)
		times, text, header := parseLine(line, &offset)
		for _, ms := range times {
			timed = append(timed, timedLine{ms: ms, text: text})
		}
		switch {
		case len(times) > 0:
		case text != "":
			plain = append(plain, Line{Text: text})
		case header:
		case len(plain) > 0 && plain[len(plain)-1].Text != "":
			plain = append(plain, Line{})
		}
	}

	if len(timed) == 0 {
		if len(plain) > maxLines {
			plain = plain[:maxLines]
		}
		// The file, or the cut, may end between two stanzas.
		if n := len(plain); n > 0 && plain[n-1].Text == "" {
			plain = plain[:n-1]
		}
		return Lyrics{Lines: plain}
	}

	// The order is that of the tags, not of the times after the offset:
	// the lines an offset brings to zero stay in the order of the song.
	// Shifting keeps the order, so the times returned are ordered too.
	slices.SortStableFunc(timed, func(a, b timedLine) int {
		switch {
		case a.ms < b.ms:
			return -1
		case a.ms > b.ms:
			return 1
		}
		return 0
	})
	if len(timed) > maxLines {
		timed = timed[:maxLines]
	}
	times := make([]int64, len(timed))
	lines := make([]Line, len(timed))
	for i, l := range timed {
		times[i] = shift(l.ms, offset)
		lines[i] = Line{TimeMS: &times[i], Text: l.text}
	}
	return Lyrics{Synced: true, Lines: lines}
}

// cutLine splits s at its first line ending. "\r\n" is one ending: read as
// two, a file written on Windows would have an empty line after every line.
func cutLine(s string) (line, rest string) {
	i := strings.IndexAny(s, "\r\n")
	if i < 0 {
		return s, ""
	}
	if strings.HasPrefix(s[i:], "\r\n") {
		return s[:i], s[i+2:]
	}
	return s[:i], s[i+1:]
}

// parseLine reads the tags a line begins with and returns its time tags, in
// milliseconds, its text, and whether it had a header tag. An offset tag
// sets *offset.
func parseLine(line string, offset *int64) (times []int64, text string, header bool) {
	rest := strings.TrimSpace(line)
	for {
		tag, after, ok := leadingTag(rest)
		if !ok {
			break
		}
		if ms, ok := parseTime(tag); ok {
			times = append(times, ms)
		} else if key, value, ok := identTag(tag); ok {
			header = true
			if key == offsetKey {
				// An offset that is not a number is ignored, as any
				// other identification tag.
				if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
					*offset = n
				}
			}
		} else {
			break
		}
		rest = strings.TrimLeftFunc(after, unicode.IsSpace)
	}
	return times, strings.TrimSpace(stripWordTags(rest)), header
}

// leadingTag splits "[tag]after".
func leadingTag(s string) (tag, after string, ok bool) {
	if !strings.HasPrefix(s, "[") {
		return "", "", false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return "", "", false
	}
	return s[1:end], s[end+1:], true
}

// identTag splits "key:value" when the key is one of the header tags, in
// any case.
func identTag(tag string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(tag, ":")
	if !ok {
		return "", "", false
	}
	key = strings.Map(lowerASCII, key)
	if !slices.Contains(identKeys, key) {
		return "", "", false
	}
	return key, value, true
}

// lowerASCII folds the case of the ASCII letters only: the Unicode folding
// would also read as a key a word with a dotted capital I in it.
func lowerASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 'a' - 'A'
	}
	return r
}

// parseTime reads the inside of a time tag: "mm:ss", "mm:ss.xx", "mm:ss:xx"
// or "hh:mm:ss.xx", with a fraction of one to three digits. A time that
// does not fit in an int64 of milliseconds is not a time.
func parseTime(tag string) (ms int64, ok bool) {
	whole, fraction, hasFraction := strings.Cut(tag, ".")
	fields := strings.Split(whole, ":")
	switch {
	case len(fields) == 2:
	case len(fields) == 3 && hasFraction:
	case len(fields) == 3:
		// Without a point the third number is the fraction, not the
		// seconds after hours and minutes: that is how the files that
		// write three numbers mean them. A file cannot say which of the
		// two it means, and a real hh:mm:ss is read the same way.
		fraction, hasFraction = fields[2], true
		fields = fields[:2]
	default:
		return 0, false
	}
	if hasFraction {
		if len(fraction) > 3 || !digits(fraction) {
			return 0, false
		}
		ms = small(fraction)
		for range 3 - len(fraction) {
			ms *= 10
		}
	}

	// Every field after the first has two digits and is at most 59.
	for _, field := range fields[1:] {
		if len(field) != 2 || !digits(field) || field > "59" {
			return 0, false
		}
	}
	ms += small(fields[len(fields)-1]) * 1000
	unit := int64(60_000)
	if len(fields) == 3 {
		ms += small(fields[1]) * 60_000
		unit = 3_600_000
	}

	if !digits(fields[0]) {
		return 0, false
	}
	first, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || first > (math.MaxInt64-ms)/unit {
		return 0, false
	}
	return ms + first*unit, true
}

// digits reports whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// small is the value of a few ASCII digits.
func small(digits string) int64 {
	var n int64
	for i := range len(digits) {
		n = n*10 + int64(digits[i]-'0')
	}
	return n
}

// stripWordTags removes the word time tags of "enhanced" LRC, <mm:ss.xx>
// in any form of a time tag, from a text. Angle brackets around anything
// else are text.
func stripWordTags(s string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(s, '<')
		if open < 0 {
			break
		}
		// A tag holds no bracket: stopping at the next one of either
		// kind keeps the reading linear in a text made of "<".
		next := strings.IndexAny(s[open+1:], "<>")
		if next < 0 {
			break
		}
		end := open + 1 + next
		_, isTime := parseTime(s[open+1 : end])
		switch {
		case s[end] == '<':
			b.WriteString(s[:end])
			s = s[end:]
		case isTime:
			b.WriteString(s[:open])
			s = s[end+1:]
		default:
			b.WriteString(s[:end+1])
			s = s[end+1:]
		}
	}
	b.WriteString(s)
	return b.String()
}

// shift applies the offset of a file to a time: a positive offset makes the
// lyrics earlier. The result stays between zero and the largest int64.
func shift(ms, offset int64) int64 {
	if offset >= 0 {
		return max(ms-offset, 0)
	}
	if ms > math.MaxInt64+offset {
		return math.MaxInt64
	}
	return ms - offset
}
