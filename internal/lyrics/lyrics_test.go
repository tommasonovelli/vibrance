package lyrics

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const bom = "\xEF\xBB\xBF"

// line is a Line with its time as a value: untimed stands for a nil TimeMS.
type line struct {
	ms   int64
	text string
}

const untimed = int64(-1)

func at(ms int64, text string) line { return line{ms: ms, text: text} }
func plain(text string) line        { return line{ms: untimed, text: text} }

func flat(l Lyrics) []line {
	out := make([]line, 0, len(l.Lines))
	for _, x := range l.Lines {
		if x.TimeMS == nil {
			out = append(out, plain(x.Text))
		} else {
			out = append(out, at(*x.TimeMS, x.Text))
		}
	}
	return out
}

func show(synced bool, lines []line) string {
	var b strings.Builder
	fmt.Fprintf(&b, "synced=%t, %d lines", synced, len(lines))
	for i, l := range lines {
		if i == 20 {
			b.WriteString("\n  ...")
			break
		}
		if l.ms == untimed {
			fmt.Fprintf(&b, "\n  null %q", l.text)
		} else {
			fmt.Fprintf(&b, "\n  %d %q", l.ms, l.text)
		}
	}
	return b.String()
}

// check compares what Parse gives for in with the lyrics wanted. A wanted
// list with a timed line is synced lyrics.
func check(t *testing.T, in string, want ...line) {
	t.Helper()
	got := Parse([]byte(in))
	if got.Lines == nil {
		t.Errorf("Parse(%q): Lines is nil", in)
	}
	synced := len(want) > 0 && want[0].ms != untimed
	if got.Synced != synced || !slices.Equal(flat(got), want) {
		t.Errorf("Parse(%q)\n got: %s\nwant: %s", in, show(got.Synced, flat(got)), show(synced, want))
	}
	// The second writing of the grammar must read every case the same way.
	compareWithModel(t, []byte(in))
}

type parseCase struct {
	name string
	in   string
	want []line
}

func runCases(t *testing.T, cases []parseCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, c.in, c.want...)
		})
	}
}

func TestParseEmptyFiles(t *testing.T) {
	runCases(t, []parseCase{
		{"no bytes", "", nil},
		{"only a byte order mark", bom, nil},
		{"only line endings", "\n\r\n\r\n\n", nil},
		{"only white space", " \t\n  \n\t", nil},
		{"only identification tags", "[ar:A]\n[ti:T]\n[offset:100]\n", nil},
	})
	if got := Parse(nil); got.Synced || got.Lines == nil || len(got.Lines) != 0 {
		t.Errorf("Parse(nil) = %+v, want no lines in a slice that is not nil", got)
	}
}

func TestParseByteOrderMark(t *testing.T) {
	runCases(t, []parseCase{
		{"before a time tag", bom + "[00:01.00]a\n", []line{at(1000, "a")}},
		{"before an identification tag", bom + "[ar:A]\nplain\n", []line{plain("plain")}},
		{"before a text", bom + "plain\n", []line{plain("plain")}},
		{"before a line ending", bom + "\r\n[00:02]b", []line{at(2000, "b")}},
		// Only the first one is a mark: a second one is a character of
		// the file, and the bracket after it does not begin the line.
		{"twice", bom + bom + "[00:01.00]a\n", []line{plain(bom + "[00:01.00]a")}},
		{"on a later line", "[00:01.00]a\n" + bom + "[00:02.00]b\n", []line{at(1000, "a")}},
		{"inside a text", "a" + bom + "b", []line{plain("a" + bom + "b")}},
	})
}

func TestParseLineEndings(t *testing.T) {
	want := []line{at(1000, "a"), at(2000, "b"), at(3000, "c")}
	runCases(t, []parseCase{
		{"LF", "[00:01]a\n[00:02]b\n[00:03]c\n", want},
		{"CRLF", "[00:01]a\r\n[00:02]b\r\n[00:03]c\r\n", want},
		{"CR", "[00:01]a\r[00:02]b\r[00:03]c\r", want},
		{"mixed", "[00:01]a\r[00:02]b\n[00:03]c\r\n", want},
		{"no ending on the last line", "[00:01]a\n[00:02]b\r\n[00:03]c", want},
		{"LF then CR are two endings", "[00:01]a\n\r[00:02]b\n\r[00:03]c", want},
		{"CR CR LF", "[00:01]a\r\r\n[00:02]b\r\r\n[00:03]c", want},
		{"blank lines between", "\n\n[00:01]a\n\n\n[00:02]b\r\n\r\n[00:03]c\n\n", want},
		{"plain text, CR", "one\rtwo\rthree", []line{plain("one"), plain("two"), plain("three")}},
		{"plain text, CRLF", "one\r\ntwo\r\n", []line{plain("one"), plain("two")}},
		{"a timed line that is only its ending", "[00:01]\r\n[00:02]\r[00:03]\n", []line{at(1000, ""), at(2000, ""), at(3000, "")}},
	})
}

func TestParseTimeTags(t *testing.T) {
	one := func(ms int64) []line { return []line{at(ms, "x")} }
	notATag := func(in string) []line { return []line{plain(in)} }
	runCases(t, []parseCase{
		// The forms of the grammar.
		{"mm:ss", "[01:02]x", one(62_000)},
		{"mm:ss.x", "[01:02.3]x", one(62_300)},
		{"mm:ss.xx", "[01:02.34]x", one(62_340)},
		{"mm:ss.xxx", "[01:02.345]x", one(62_345)},
		{"hh:mm:ss", "[01:02:03]x", one(3_723_000)},
		{"hh:mm:ss.x", "[01:02:03.4]x", one(3_723_400)},
		{"hh:mm:ss.xx", "[01:02:03.45]x", one(3_723_450)},
		{"hh:mm:ss.xxx", "[01:02:03.456]x", one(3_723_456)},

		// Fractions: tenths, hundredths, thousandths.
		{"fraction .0", "[00:00.0]x", one(0)},
		{"fraction .5", "[00:00.5]x", one(500)},
		{"fraction .05", "[00:00.05]x", one(50)},
		{"fraction .50", "[00:00.50]x", one(500)},
		{"fraction .005", "[00:00.005]x", one(5)},
		{"fraction .050", "[00:00.050]x", one(50)},
		{"fraction .500", "[00:00.500]x", one(500)},
		{"fraction .999", "[00:00.999]x", one(999)},
		{"no digit in the fraction", "[00:01.]x", notATag("[00:01.]x")},
		{"four digits in the fraction", "[00:01.2345]x", notATag("[00:01.2345]x")},
		{"a letter in the fraction", "[00:01.2a]x", notATag("[00:01.2a]x")},
		{"two fractions", "[00:01.2.3]x", notATag("[00:01.2.3]x")},
		{"a comma for the fraction", "[00:01,23]x", notATag("[00:01,23]x")},
		{"a colon for the fraction is hours", "[00:01:23]x", one(83_000)},
		{"a colon for the fraction, over 59", "[00:01:75]x", notATag("[00:01:75]x")},

		// Minutes of any length.
		{"zero", "[00:00.00]x", one(0)},
		{"one digit of minutes", "[5:07]x", one(307_000)},
		{"three digits of minutes", "[123:45.6]x", one(7_425_600)},
		{"minutes over 59", "[75:00]x", one(4_500_000)},
		{"many zeros", "[0000000000000000000000000:01]x", one(1000)},
		{"no minutes", "[:01]x", notATag("[:01]x")},
		{"only seconds", "[01]x", notATag("[01]x")},
		{"only a fraction", "[.5]x", notATag("[.5]x")},
		{"empty", "[]x", notATag("[]x")},

		// Seconds: two digits, 0 to 59.
		{"second 59", "[00:59.99]x", one(59_990)},
		{"second 60", "[00:60]x", notATag("[00:60]x")},
		{"second 99", "[00:99.00]x", notATag("[00:99.00]x")},
		{"one digit of seconds", "[00:5]x", notATag("[00:5]x")},
		{"three digits of seconds", "[00:005]x", notATag("[00:005]x")},
		{"no seconds", "[00:]x", notATag("[00:]x")},

		// With hours, minutes too are two digits, 0 to 59.
		{"one digit of hours", "[1:00:00]x", one(3_600_000)},
		{"many digits of hours", "[100:00:00]x", one(360_000_000)},
		{"minute 59 after hours", "[0:59:59.999]x", one(3_599_999)},
		{"minute 60 after hours", "[00:60:00]x", notATag("[00:60:00]x")},
		{"one digit of minutes after hours", "[00:1:00]x", notATag("[00:1:00]x")},
		{"no hours", "[:00:01]x", notATag("[:00:01]x")},
		{"four fields", "[00:00:00:01]x", notATag("[00:00:00:01]x")},

		// Nothing but ASCII digits.
		{"a sign", "[-1:00]x", notATag("[-1:00]x")},
		{"a plus", "[+1:00]x", notATag("[+1:00]x")},
		{"a space inside", "[ 00:01.00 ]x", notATag("[ 00:01.00 ]x")},
		{"a space after the colon", "[00: 01]x", notATag("[00: 01]x")},
		{"letters", "[mm:ss.xx]x", notATag("[mm:ss.xx]x")},
		{"an underscore", "[1_0:00]x", notATag("[1_0:00]x")},
		{"hexadecimal", "[0x1:00]x", notATag("[0x1:00]x")},
		{"digits of another script", "[٠٠:٠١]x", notATag("[٠٠:٠١]x")},
		{"full width digits", "[００:０１]x", notATag("[００:０１]x")},

		// Brackets.
		{"not closed", "[00:01.00 x", notATag("[00:01.00 x")},
		{"not opened", "00:01.00]x", notATag("00:01.00]x")},
		{"round brackets", "(00:01.00)x", notATag("(00:01.00)x")},
		{"a bracket inside", "[[00:01.00]]x", notATag("[[00:01.00]]x")},
		{"a tag after the text", "x[00:01.00]", notATag("x[00:01.00]")},
	})
}

func TestParseTimesThatDoNotFit(t *testing.T) {
	// 153722867280912:55.807 is the largest int64 of milliseconds.
	check(t, "[153722867280912:55.807]x", at(math.MaxInt64, "x"))
	check(t, "[2562047788015:12:55.807]x", at(math.MaxInt64, "x"))
	check(t, "[153722867280912:55.806]x", at(math.MaxInt64-1, "x"))
	for _, in := range []string{
		"[153722867280912:55.808]x",
		"[153722867280912:56]x",
		"[153722867280913:00]x",
		"[2562047788015:12:55.808]x",
		"[2562047788015:13:00]x",
		"[2562047788016:00:00]x",
		"[9223372036854775807:00]x",
		"[9223372036854775808:00]x",
		"[" + strings.Repeat("9", 400) + ":00]x",
		"[" + strings.Repeat("9", 400) + ":00:00.999]x",
	} {
		check(t, in, plain(in))
	}
	// A time that fits stays within an int64 under any offset.
	check(t, "[offset:-1]\n[153722867280912:55.807]x\n[00:00]y", at(1, "y"), at(math.MaxInt64, "x"))
	check(t, "[offset:-9223372036854775808]\n[00:00]y\n[00:01]x", at(math.MaxInt64, "y"), at(math.MaxInt64, "x"))
	check(t, "[offset:-9223372036854775807]\n[00:00]y\n[00:00.001]x", at(math.MaxInt64, "y"), at(math.MaxInt64, "x"))
	check(t, "[offset:-9223372036854775806]\n[00:00]y\n[00:00.001]x", at(math.MaxInt64-1, "y"), at(math.MaxInt64, "x"))
	check(t, "[offset:9223372036854775807]\n[153722867280912:55.807]x\n[00:00]y", at(0, "y"), at(0, "x"))
}

func TestParseSeveralTimeTags(t *testing.T) {
	runCases(t, []parseCase{
		{"two tags", "[00:10.00][00:20.00]chorus", []line{at(10_000, "chorus"), at(20_000, "chorus")}},
		{"tags out of order", "[00:20.00][00:10.00]chorus", []line{at(10_000, "chorus"), at(20_000, "chorus")}},
		{"a chorus among verses",
			"[00:05]one\n[00:10][00:30][00:50]chorus\n[00:20]two\n[00:40]three",
			[]line{at(5000, "one"), at(10_000, "chorus"), at(20_000, "two"), at(30_000, "chorus"), at(40_000, "three"), at(50_000, "chorus")}},
		{"the same tag twice", "[00:10][00:10]x", []line{at(10_000, "x"), at(10_000, "x")}},
		{"tags of different forms", "[1:00][00:00:30.5][00:15.250]x", []line{at(15_250, "x"), at(30_500, "x"), at(60_000, "x")}},
		{"white space between the tags", "[00:10.00] [00:20.00]\t[00:30.00]  chorus", []line{at(10_000, "chorus"), at(20_000, "chorus"), at(30_000, "chorus")}},
		{"several tags and no text", "[00:10][00:20]", []line{at(10_000, ""), at(20_000, "")}},
		{"a bracket that is not a tag ends the tags", "[00:10][Chorus][00:20]x", []line{at(10_000, "[Chorus][00:20]x")}},
		{"a time that is not valid ends the tags", "[00:10][00:75][00:20]x", []line{at(10_000, "[00:75][00:20]x")}},
		{"a tag after the text is text", "[00:10]one [00:20]two", []line{at(10_000, "one [00:20]two")}},
	})
}

func TestParseOffset(t *testing.T) {
	runCases(t, []parseCase{
		{"positive: the lyrics come earlier", "[offset:+500]\n[00:01.00]a\n[00:02.00]b", []line{at(500, "a"), at(1500, "b")}},
		{"without a sign is positive", "[offset:500]\n[00:01.00]a", []line{at(500, "a")}},
		{"negative: the lyrics come later", "[offset:-500]\n[00:01.00]a\n[00:02.00]b", []line{at(1500, "a"), at(2500, "b")}},
		{"zero", "[offset:0]\n[00:01.00]a", []line{at(1000, "a")}},
		{"minus zero", "[offset:-0]\n[00:01.00]a", []line{at(1000, "a")}},
		{"never below zero", "[offset:1500]\n[00:01.00]a\n[00:02.00]b", []line{at(0, "a"), at(500, "b")}},
		{"exactly to zero", "[offset:1000]\n[00:01.00]a", []line{at(0, "a")}},
		{"one millisecond", "[offset:1]\n[00:01.00]a", []line{at(999, "a")}},
		{"after the lines it moves", "[00:01.00]a\n[00:02.00]b\n[offset:-250]", []line{at(1250, "a"), at(2250, "b")}},
		{"between the lines", "[00:01.00]a\n[offset:250]\n[00:02.00]b", []line{at(750, "a"), at(1750, "b")}},
		{"the last one counts", "[offset:100]\n[00:01.00]a\n[offset:-200]", []line{at(1200, "a")}},
		{"white space around the number", "[offset: +500 ]\n[00:01.00]a", []line{at(500, "a")}},
		{"upper case", "[OFFSET:500]\n[00:01.00]a", []line{at(500, "a")}},
		{"mixed case", "[Offset:500]\n[00:01.00]a", []line{at(500, "a")}},
		{"on the line of a time tag", "[offset:500][00:01.00]a", []line{at(500, "a")}},
		{"after a time tag", "[00:01.00][offset:500]a", []line{at(500, "a")}},
		{"with a byte order mark and CRLF", bom + "[offset:500]\r\n[00:01.00]a\r\n", []line{at(500, "a")}},

		{"not a number", "[offset:abc]\n[00:01.00]a", []line{at(1000, "a")}},
		{"empty", "[offset:]\n[00:01.00]a", []line{at(1000, "a")}},
		{"a fraction", "[offset:1.5]\n[00:01.00]a", []line{at(1000, "a")}},
		{"a unit", "[offset:500ms]\n[00:01.00]a", []line{at(1000, "a")}},
		{"two signs", "[offset:+-500]\n[00:01.00]a", []line{at(1000, "a")}},
		{"a space after the sign", "[offset:- 500]\n[00:01.00]a", []line{at(1000, "a")}},
		{"an underscore", "[offset:5_00]\n[00:01.00]a", []line{at(1000, "a")}},
		{"too large", "[offset:9223372036854775808]\n[00:01.00]a", []line{at(1000, "a")}},
		{"too small", "[offset:-9223372036854775809]\n[00:01.00]a", []line{at(1000, "a")}},
		{"one that is not valid leaves the one before", "[offset:500]\n[offset:x]\n[00:01.00]a", []line{at(500, "a")}},
		{"a space before the colon is not the tag", "[offset :500]\n[00:01.00]a", []line{at(1000, "a")}},

		{"in a file without time tags it is no line", "[offset:500]\none\ntwo", []line{plain("one"), plain("two")}},
		{"the text after it is text", "[offset:500]one", []line{plain("one")}},
	})
}

func TestParseOrder(t *testing.T) {
	runCases(t, []parseCase{
		{"out of order", "[00:03]c\n[00:01]a\n[00:02]b", []line{at(1000, "a"), at(2000, "b"), at(3000, "c")}},
		{"equal times keep the order of the file",
			"[00:02]first\n[00:01]x\n[00:02]second\n[00:02]third\n[00:02.000]fourth",
			[]line{at(1000, "x"), at(2000, "first"), at(2000, "second"), at(2000, "third"), at(2000, "fourth")}},
		{"equal times on one line and on others",
			"[00:02][00:01]a\n[00:01][00:02]b",
			[]line{at(1000, "a"), at(1000, "b"), at(2000, "a"), at(2000, "b")}},
		// The lines an offset brings to zero are still in the order of
		// their tags, not of the file.
		{"the lines brought to zero stay in the order of the song",
			"[offset:5000]\n[00:03]c\n[00:01]a\n[00:06]d\n[00:02]b",
			[]line{at(0, "a"), at(0, "b"), at(0, "c"), at(1000, "d")}},
	})
}

// Enough lines of few times that a sort which is not stable would move
// them: within a time, the order is that of the file.
func TestParseOrderIsStableOnManyLines(t *testing.T) {
	const n = 3000
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "[00:%02d]%d\n", (n-i)%7, i)
	}
	got := Parse([]byte(b.String()))
	if !got.Synced || len(got.Lines) != n {
		t.Fatalf("synced=%t with %d lines", got.Synced, len(got.Lines))
	}
	lastTime, lastIndex := int64(-1), -1
	for i, l := range got.Lines {
		var index int
		if _, err := fmt.Sscanf(l.Text, "%d", &index); err != nil {
			t.Fatal(err)
		}
		switch {
		case *l.TimeMS < lastTime:
			t.Fatalf("line %d: the time %d comes after %d", i, *l.TimeMS, lastTime)
		case *l.TimeMS == lastTime && index < lastIndex:
			t.Fatalf("line %d: at the time %d the line %d of the file comes after the line %d", i, *l.TimeMS, index, lastIndex)
		}
		lastTime, lastIndex = *l.TimeMS, index
	}
}

func TestParseWordTags(t *testing.T) {
	runCases(t, []parseCase{
		{"enhanced LRC", "[00:01.00]<00:01.00>one <00:01.50>two <00:02.00>three<00:02.50>", []line{at(1000, "one two three")}},
		{"every form of time", "[00:01]a<1:02>b<01:02.3>c<01:02.34>d<01:02.345>e<01:02:03.45>f", []line{at(1000, "abcdef")}},
		{"only word tags", "[00:01.00]<00:01.00><00:02.00>", []line{at(1000, "")}},
		{"white space left by the tags at the ends is trimmed", "[00:01.00] <00:01.00> one <00:02.00> ", []line{at(1000, "one")}},
		{"white space left inside is kept", "[00:01.00]one <00:01.50> two", []line{at(1000, "one  two")}},
		{"in a file without time tags", "<00:01.00>one <00:02.00>two\nthree", []line{plain("one two"), plain("three")}},
		{"a line of word tags only, without time tags", "<00:01.00><00:02.00>\ntext", []line{plain("text")}},
		{"word tags do not make a file synced", "<00:01.00>one", []line{plain("one")}},
		{"at the start of a line it is not a time tag", "<00:05.00>[00:01.00]x", []line{plain("[00:01.00]x")}},

		{"a heart", "[00:01]I <3 you", []line{at(1000, "I <3 you")}},
		{"markup", "[00:01]<i>soft</i>", []line{at(1000, "<i>soft</i>")}},
		{"comparison", "[00:01]a < b > c", []line{at(1000, "a < b > c")}},
		{"empty angle brackets", "[00:01]a<>b", []line{at(1000, "a<>b")}},
		{"not closed", "[00:01]a<00:02.00", []line{at(1000, "a<00:02.00")}},
		{"not opened", "[00:01]a00:02.00>b", []line{at(1000, "a00:02.00>b")}},
		{"a time that is not valid", "[00:01]a<00:75.00>b", []line{at(1000, "a<00:75.00>b")}},
		{"four digits of fraction", "[00:01]a<00:02.0000>b", []line{at(1000, "a<00:02.0000>b")}},
		{"spaces inside", "[00:01]a< 00:02.00 >b", []line{at(1000, "a< 00:02.00 >b")}},
		{"nested", "[00:01]a<<00:02.00>>b", []line{at(1000, "a<>b")}},
		{"an open bracket before a tag", "[00:01]a<b<00:02.00>c", []line{at(1000, "a<bc")}},
		{"a closing bracket after a tag", "[00:01]a<00:02.00>>b", []line{at(1000, "a>b")}},
		{"what the removal brings together is not read again", "[00:01]<00:<00:02.00>03.00>x", []line{at(1000, "<00:03.00>x")}},
		{"square tags inside the text stay", "[00:01]a[00:02]b<00:03>c", []line{at(1000, "a[00:02]bc")}},
		{"an identification tag in angle brackets", "[00:01]a<ar:x>b", []line{at(1000, "a<ar:x>b")}},
	})
}

func TestParseEmptyTimedLines(t *testing.T) {
	runCases(t, []parseCase{
		{"kept between the lines", "[00:01]a\n[00:05]\n[00:09]b", []line{at(1000, "a"), at(5000, ""), at(9000, "b")}},
		{"kept at the start and at the end", "[00:00]\n[00:05]a\n[00:09]", []line{at(0, ""), at(5000, "a"), at(9000, "")}},
		{"white space only is empty", "[00:05] \t \n[00:09]b", []line{at(5000, ""), at(9000, "b")}},
		{"a file of empty timed lines is synced", "[00:01]\n[00:02]\n", []line{at(1000, ""), at(2000, "")}},
		{"a single one", "[00:00.00]", []line{at(0, "")}},
	})
}

func TestParseText(t *testing.T) {
	runCases(t, []parseCase{
		{"white space around the text", "[00:01]  two words \t", []line{at(1000, "two words")}},
		{"white space inside the text", "[00:01]two   words", []line{at(1000, "two   words")}},
		{"white space before the tag", " \t[00:01]a", []line{at(1000, "a")}},
		{"other white space", "\xc2\xa0[00:01]\xe3\x80\x80a\xe2\x80\x83", []line{at(1000, "a")}},
		{"letters of many scripts", "[00:01]Così è, naïve: 東京 Ελλάδα 🎵", []line{at(1000, "Così è, naïve: 東京 Ελλάδα 🎵")}},
		{"control characters stay", "[00:01]a\x00b\x1fc", []line{at(1000, "a\x00b\x1fc")}},
		{"a closing bracket", "[00:01]]a", []line{at(1000, "]a")}},
		{"singer prefixes are text", "[00:01]F: hello", []line{at(1000, "F: hello")}},
	})
}

func TestParseBytesThatAreNotUTF8(t *testing.T) {
	const r = string(utf8.RuneError)
	runCases(t, []parseCase{
		{"in a timed text", "[00:01]caf\xe9", []line{at(1000, "caf"+r)}},
		{"in a plain text", "na\xefve\nb", []line{plain("na" + r + "ve"), plain("b")}},
		{"a run of bytes is one character", "[00:01]a\xff\xfe\xfdb", []line{at(1000, "a"+r+"b")}},
		{"a truncated sequence", "[00:01]a\xe6\x9d", []line{at(1000, "a"+r)}},
		{"in a tag it is not a tag", "[00:\xff01]a", []line{plain("[00:" + r + "01]a")}},
		{"across a line ending", "a\xff\n\xffb", []line{plain("a" + r), plain(r + "b")}},
		{"a UTF-16 file is not read as lyrics", "\xff\xfe[\x000\x000\x00", []line{plain(r + "[\x000\x000\x00")}},
	})
}

func TestParseFilesWithoutTimeTags(t *testing.T) {
	runCases(t, []parseCase{
		{"plain lines", "one\ntwo\nthree\n", []line{plain("one"), plain("two"), plain("three")}},
		{"empty lines are dropped", "one\n\n\ntwo\n \t \nthree\n\n", []line{plain("one"), plain("two"), plain("three")}},
		{"the text is trimmed", "  one \n\ttwo\t", []line{plain("one"), plain("two")}},
		{"section headers are text", "[Chorus]\nla la\n[Verse 1: Someone]\nla", []line{plain("[Chorus]"), plain("la la"), plain("[Verse 1: Someone]"), plain("la")}},
		{"times that are not valid are text", "[00:75.00]a\n[1:2]b", []line{plain("[00:75.00]a"), plain("[1:2]b")}},
		{"the order of the file is kept", "c\na\nb\na", []line{plain("c"), plain("a"), plain("b"), plain("a")}},
		{"one line", "only", []line{plain("only")}},
	})
}

func TestParseIdentificationTags(t *testing.T) {
	runCases(t, []parseCase{
		{"ignored in a file without time tags",
			"[ar:Artist]\n[ti:Title]\n[al:Album]\n[by:Someone]\n[length:03:20]\none\ntwo",
			[]line{plain("one"), plain("two")}},
		{"ignored in a file with time tags",
			"[ar:Artist]\n[ti:Title]\n[al:Album]\n[by:Someone]\n[length:03:20]\n[00:01]one",
			[]line{at(1000, "one")}},
		{"in any case", "[AR:Artist]\n[Ti:Title]\n[aL:Album]\n[BY:x]\n[LENGTH:1]\none", []line{plain("one")}},
		{"with an empty value", "[ar:]\n[ti:]\none", []line{plain("one")}},
		{"with colons in the value", "[ti:a:b:c]\none", []line{plain("one")}},
		{"several on a line", "[ar:A][ti:T] [al:L]\none", []line{plain("one")}},
		{"with a text after it", "[ar:Artist]one", []line{plain("one")}},
		{"before a time tag", "[ar:Artist][00:01]one", []line{at(1000, "one")}},
		{"with white space before it", "  [ar:Artist]\none", []line{plain("one")}},
		{"a length that looks like a time", "[length:03:20]\none", []line{plain("one")}},
		{"the value ends at the first closing bracket", "[ti:Song [live]]\none", []line{plain("]"), plain("one")}},

		// Only the tags of the grammar: anything else in brackets is text.
		{"other keys are text", "[re:Editor]\n[ve:1.0]\n[au:Author]\n[la:en]\none",
			[]line{plain("[re:Editor]"), plain("[ve:1.0]"), plain("[au:Author]"), plain("[la:en]"), plain("one")}},
		{"other keys are dropped with the untimed lines", "[re:Editor]\n[00:01]one", []line{at(1000, "one")}},
		{"a key with a space", "[ar :Artist]", []line{plain("[ar :Artist]")}},
		{"a key without a colon", "[ar]", []line{plain("[ar]")}},
		{"a longer key", "[artist:A]", []line{plain("[artist:A]")}},
		{"a dotted capital I is not an i", "[tİ:T]", []line{plain("[tİ:T]")}},
		{"a long s is not an s", "[offſet:500]\n[00:01]a", []line{at(1000, "a")}},
	})
}

func TestParseUntimedLinesInASyncedFile(t *testing.T) {
	runCases(t, []parseCase{
		{"dropped", "intro\n[00:01]one\nbetween\n[00:02]two\nend", []line{at(1000, "one"), at(2000, "two")}},
		{"dropped before the first tag is seen", "a\nb\nc\n[00:01]one", []line{at(1000, "one")}},
		{"a single time tag is enough", strings.Repeat("text\n", 50) + "[00:00]", []line{at(0, "")}},
		{"headers and tags that are not valid are dropped too", "[Chorus]\n[00:75]x\n[00:01]one", []line{at(1000, "one")}},
	})
}

// The file MusicLib wrote in the fixture library.
func TestParseFixtureLibrary(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "library-v1", "Aurora Sines", "Alpha_ Light_", "01 - First Light.lrc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	check(t, string(data), at(0, "The first line"), at(1000, "The second line"))
}

// A file with everything T17 lists at once.
func TestParseDirtyFile(t *testing.T) {
	in := bom + "[ti:Dirty Song]\r\n" +
		"[ar:Someone]\r\n" +
		"[offset:+250]\r\n" +
		"[re:Some Editor]\r\n" +
		"\r\n" +
		"Lyrics by nobody\r\n" +
		"[00:12.00][01:15.50] <00:12.00>Chorus <00:12.80>line \r\n" +
		"[00:05.5]First\r" +
		"[00:20]\r\n" +
		"   [00:17.123]Third  \n" +
		"[00:00.10]Early\r\n" +
		"[1:02:03.4]Late\r\n" +
		"[00:99.00]never\r\n" +
		"[01:15.50]Same time, later in the file"
	check(t, in,
		at(0, "Early"),
		at(5250, "First"),
		at(11_750, "Chorus line"),
		at(16_873, "Third"),
		at(19_750, ""),
		at(75_250, "Chorus line"),
		at(75_250, "Same time, later in the file"),
		at(3_723_150, "Late"),
	)
}

func TestParseCutsAtMaxLines(t *testing.T) {
	if maxLines != 10000 {
		t.Fatalf("maxLines = %d, want 10000", maxLines)
	}
	timedLines := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "[%d:%02d.%03d]line %d\n", i/60000, i/1000%60, i%1000, i)
		}
		return b.String()
	}
	plainLines := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	reversed := func(s string) string {
		lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		slices.Reverse(lines)
		return strings.Join(lines, "\n")
	}
	expectTimed := func(t *testing.T, got Lyrics, n int) {
		t.Helper()
		if !got.Synced || len(got.Lines) != n {
			t.Fatalf("synced=%t with %d lines, want synced with %d", got.Synced, len(got.Lines), n)
		}
		for i, l := range got.Lines {
			if l.TimeMS == nil || *l.TimeMS != int64(i) || l.Text != fmt.Sprintf("line %d", i) {
				t.Fatalf("line %d is %s", i, show(true, flat(got)[i:i+1]))
			}
		}
	}
	expectPlain := func(t *testing.T, got Lyrics, n int) {
		t.Helper()
		if got.Synced || len(got.Lines) != n {
			t.Fatalf("synced=%t with %d lines, want not synced with %d", got.Synced, len(got.Lines), n)
		}
		for i, l := range got.Lines {
			if l.TimeMS != nil || l.Text != fmt.Sprintf("line %d", i) {
				t.Fatalf("line %d is %s", i, show(false, flat(got)[i:i+1]))
			}
		}
	}

	t.Run("one line less", func(t *testing.T) {
		expectTimed(t, Parse([]byte(timedLines(maxLines-1))), maxLines-1)
		expectPlain(t, Parse([]byte(plainLines(maxLines-1))), maxLines-1)
	})
	t.Run("exactly", func(t *testing.T) {
		expectTimed(t, Parse([]byte(timedLines(maxLines))), maxLines)
		expectPlain(t, Parse([]byte(plainLines(maxLines))), maxLines)
	})
	t.Run("one line more", func(t *testing.T) {
		expectTimed(t, Parse([]byte(timedLines(maxLines+1))), maxLines)
		expectPlain(t, Parse([]byte(plainLines(maxLines+1))), maxLines)
	})
	t.Run("many more", func(t *testing.T) {
		expectTimed(t, Parse([]byte(timedLines(5*maxLines))), maxLines)
		expectPlain(t, Parse([]byte(plainLines(5*maxLines))), maxLines)
	})
	// The cut comes after the ordering: the lines kept are the earliest
	// of the song, not the first of the file.
	t.Run("the earliest lines are kept", func(t *testing.T) {
		expectTimed(t, Parse([]byte(reversed(timedLines(maxLines+500)))), maxLines)
	})
	t.Run("the tags of one line count as lines", func(t *testing.T) {
		var b strings.Builder
		for i := range maxLines + 1 {
			fmt.Fprintf(&b, "[%d:%02d.%03d]", i/60000, i/1000%60, i%1000)
		}
		got := Parse([]byte(b.String() + "x"))
		if !got.Synced || len(got.Lines) != maxLines {
			t.Fatalf("synced=%t with %d lines, want synced with %d", got.Synced, len(got.Lines), maxLines)
		}
		for i, l := range got.Lines {
			if *l.TimeMS != int64(i) || l.Text != "x" {
				t.Fatalf("line %d is %s", i, show(true, flat(got)[i:i+1]))
			}
		}
	})
	t.Run("the lines that are dropped do not count", func(t *testing.T) {
		// Empty lines, identification tags and, in a synced file, lines
		// without a time are not lines of the lyrics.
		in := strings.Repeat("\n[ar:x]\n", maxLines) + plainLines(maxLines)
		expectPlain(t, Parse([]byte(in)), maxLines)
		in = plainLines(3*maxLines) + timedLines(maxLines)
		expectTimed(t, Parse([]byte(in)), maxLines)
	})
	t.Run("an offset after the cut still counts", func(t *testing.T) {
		got := Parse([]byte(timedLines(maxLines+10) + "[offset:-7]\n"))
		if len(got.Lines) != maxLines || *got.Lines[0].TimeMS != 7 || *got.Lines[maxLines-1].TimeMS != maxLines+6 {
			t.Fatalf("%s", show(got.Synced, flat(got)))
		}
	})
}

// Each line has its own time: changing one must not change another.
func TestParseTimesAreNotShared(t *testing.T) {
	got := Parse([]byte("[00:01][00:01]a\n[00:01]b"))
	if len(got.Lines) != 3 {
		t.Fatalf("%s", show(got.Synced, flat(got)))
	}
	*got.Lines[0].TimeMS = 77
	if *got.Lines[1].TimeMS != 1000 || *got.Lines[2].TimeMS != 1000 {
		t.Fatalf("the lines share a time: %s", show(got.Synced, flat(got)))
	}
}

// The data given to Parse is not changed and not kept.
func TestParseLeavesItsInput(t *testing.T) {
	in := []byte(bom + "[00:01]a\r\nna\xefve\n")
	saved := slices.Clone(in)
	got := Parse(in)
	if !slices.Equal(in, saved) {
		t.Fatalf("the input became %q", in)
	}
	for i := range in {
		in[i] = 'Z'
	}
	if want := []line{at(1000, "a")}; !slices.Equal(flat(got), want) {
		t.Fatalf("after the input changed: %s", show(got.Synced, flat(got)))
	}
}

// A file of the largest size the server reads (2 MiB, §9.3) made to cost
// the most must still be read in a time that grows with its size: each of
// these would take minutes with a scan that starts again at every bracket.
func TestParseTimeIsLinear(t *testing.T) {
	const size = 2 << 20
	inputs := map[string]string{
		"open angle brackets":          strings.Repeat("<", size) + ">",
		"timed open angle brackets":    "[00:01]" + strings.Repeat("<", size) + "00:01>",
		"angle brackets with digits":   strings.Repeat("<00:0", size/5) + ">",
		"open square brackets":         strings.Repeat("[", size) + "]",
		"square brackets, one a line":  strings.Repeat("[\n", size/2) + "]",
		"time tags on one line":        strings.Repeat("[00:00]", size/7),
		"word tags on one line":        strings.Repeat("<00:00>", size/7),
		"identification tags":          strings.Repeat("[ar:x]", size/6),
		"offsets":                      strings.Repeat("[offset:1]", size/10),
		"carriage returns":             strings.Repeat("\r", size),
		"white space":                  strings.Repeat(" ", size) + "x",
		"white space between tags":     strings.Repeat("[00:00] ", size/8),
		"one digit a line":             strings.Repeat("1\n", size/2),
		"digits":                       "[" + strings.Repeat("9", size) + ":00]",
		"bytes that are not UTF-8":     strings.Repeat("\xff", size),
		"closing brackets":             strings.Repeat("]", size),
		"closing angle brackets":       "<" + strings.Repeat(">", size),
		"alternating angle brackets":   strings.Repeat("<>", size/2),
		"alternating square brackets":  strings.Repeat("[]", size/2),
		"timed alternating brackets":   "[00:00]" + strings.Repeat("><", size/2),
		"equal times, reversed blocks": strings.Repeat("[00:02]b\n[00:01]a\n", size/18),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			got := Parse([]byte(in))
			if elapsed := time.Since(start); elapsed > 20*time.Second {
				t.Errorf("%d bytes took %v", len(in), elapsed)
			}
			if len(got.Lines) > maxLines {
				t.Errorf("%d lines", len(got.Lines))
			}
		})
	}
}

func TestShift(t *testing.T) {
	const most = math.MaxInt64
	for _, c := range []struct{ ms, offset, want int64 }{
		{1000, 0, 1000},
		{1000, 400, 600},
		{1000, 1000, 0},
		{1000, 1001, 0},
		{1000, most, 0},
		{0, most, 0},
		{most, most, 0},
		{most, most - 1, 1},
		{most, 0, most},
		{1000, -400, 1400},
		{0, -most, most},
		{1, -most, most},
		{0, math.MinInt64, most},
		{1, math.MinInt64, most},
		{most, math.MinInt64, most},
		{most, -1, most},
		{most - 1, -1, most},
		{most - 2, -1, most - 1},
	} {
		if got := shift(c.ms, c.offset); got != c.want {
			t.Errorf("shift(%d, %d) = %d, want %d", c.ms, c.offset, got, c.want)
		}
	}
}
