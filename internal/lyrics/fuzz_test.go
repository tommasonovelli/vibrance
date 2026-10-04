package lyrics

import (
	"bytes"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// The grammar of §9.3 written a second time, with regular expressions and
// numbers without a limit: the fuzz target compares Parse with it.
var (
	modelEnding = regexp.MustCompile(`\r\n|\r|\n`)
	modelTag    = regexp.MustCompile(`^\[([^\]]*)\]`)
	modelTime   = regexp.MustCompile(`^([0-9]+):(?:([0-5][0-9]):)?([0-5][0-9])(?:\.([0-9]{1,3}))?$`)
	modelIdent  = regexp.MustCompile(`^(?:[aA][rR]|[tT][iI]|[aA][lL]|[bB][yY]|[lL][eE][nN][gG][tT][hH]|([oO][fF][fF][sS][eE][tT])):`)
	modelNumber = regexp.MustCompile(`^[+-]?[0-9]+$`)
	modelWord   = regexp.MustCompile(`<([^<>]*)>`)

	maxInt64 = big.NewInt(math.MaxInt64)
	minInt64 = big.NewInt(math.MinInt64)
)

func bigOf(digits string) *big.Int {
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		panic("not a number: " + digits)
	}
	return n
}

// modelTimeOf is the time a tag stands for, when it is one.
func modelTimeOf(tag string) (*big.Int, bool) {
	m := modelTime.FindStringSubmatch(tag)
	if m == nil {
		return nil, false
	}
	first, minutes, seconds, fraction := m[1], m[2], m[3], m[4]
	ms := new(big.Int)
	if minutes == "" {
		ms.Mul(bigOf(first), big.NewInt(60_000))
	} else {
		ms.Mul(bigOf(first), big.NewInt(3_600_000))
		ms.Add(ms, new(big.Int).Mul(bigOf(minutes), big.NewInt(60_000)))
	}
	ms.Add(ms, new(big.Int).Mul(bigOf(seconds), big.NewInt(1000)))
	if fraction != "" {
		ms.Add(ms, bigOf((fraction + "00")[:3]))
	}
	return ms, ms.Cmp(maxInt64) <= 0
}

type modelLine struct {
	ms   *big.Int
	text string
}

func modelParse(data []byte) (synced bool, lines []line) {
	data = bytes.TrimPrefix(data, []byte(bom))
	text := strings.ToValidUTF8(string(data), string(utf8.RuneError))

	var timed []modelLine
	var plainLines []line
	offset := new(big.Int)
	for _, l := range modelEnding.Split(text, -1) {
		rest := strings.TrimSpace(l)
		var times []*big.Int
		for {
			m := modelTag.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			if ms, ok := modelTimeOf(m[1]); ok {
				times = append(times, ms)
			} else if id := modelIdent.FindStringSubmatch(m[1]); id != nil {
				value := strings.TrimSpace(m[1][len(id[0]):])
				if id[1] != "" && modelNumber.MatchString(value) {
					if n := bigOf(value); n.Cmp(minInt64) >= 0 && n.Cmp(maxInt64) <= 0 {
						offset = n
					}
				}
			} else {
				break
			}
			rest = strings.TrimLeftFunc(rest[len(m[0]):], unicode.IsSpace)
		}
		words := modelWord.ReplaceAllStringFunc(rest, func(tag string) string {
			if _, ok := modelTimeOf(tag[1 : len(tag)-1]); ok {
				return ""
			}
			return tag
		})
		words = strings.TrimSpace(words)
		for _, ms := range times {
			timed = append(timed, modelLine{ms: ms, text: words})
		}
		if len(times) == 0 && words != "" {
			plainLines = append(plainLines, plain(words))
		}
	}

	if len(timed) == 0 {
		return false, plainLines[:min(len(plainLines), maxLines)]
	}
	sort.SliceStable(timed, func(i, j int) bool { return timed[i].ms.Cmp(timed[j].ms) < 0 })
	timed = timed[:min(len(timed), maxLines)]
	for _, l := range timed {
		ms := new(big.Int).Sub(l.ms, offset)
		if ms.Sign() < 0 {
			ms.SetInt64(0)
		}
		if ms.Cmp(maxInt64) > 0 {
			ms.Set(maxInt64)
		}
		lines = append(lines, at(ms.Int64(), l.text))
	}
	return true, lines
}

// The model reads the tables of the other tests as Parse does, so that it
// is not only the fuzzer that compares the two.
func TestModelAgreesOnTheSeeds(t *testing.T) {
	for _, seed := range lrcSeeds(t) {
		compareWithModel(t, seed)
	}
}

func compareWithModel(t *testing.T, data []byte) {
	t.Helper()
	got := Parse(data)
	synced, lines := modelParse(data)
	if got.Synced != synced || !slices.Equal(flat(got), lines) {
		t.Fatalf("Parse(%q)\n  got: %s\nmodel: %s", data, show(got.Synced, flat(got)), show(synced, lines))
	}
}

func lrcSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "library-v1", "Aurora Sines", "Alpha_ Light_", "01 - First Light.lrc"))
	if err != nil {
		tb.Fatal(err)
	}
	seeds := [][]byte{fixture}
	for _, s := range []string{
		"",
		bom,
		bom + "[ti:Dirty Song]\r\n[ar:Someone]\r\n[offset:+250]\r\n[re:Editor]\r\n\r\nLyrics by nobody\r\n" +
			"[00:12.00][01:15.50] <00:12.00>Chorus <00:12.80>line \r\n[00:05.5]First\r[00:20]\r\n" +
			"   [00:17.123]Third  \n[00:00.10]Early\r\n[1:02:03.4]Late\r\n[00:99.00]never\r\n[01:15.50]Same",
		"one\n\ntwo\r\n[Chorus]\r<00:01.00>three <3\n[Verse 1: Someone]\n",
		"[00:03]c\n[00:01]a\n[00:02]b\n[00:01]again\nuntimed\n[00:02]\n",
		"[offset:-9223372036854775808]\n[00:00]y\n[153722867280912:55.807]x\n[153722867280912:55.808]z",
		"[offset:9223372036854775807][offset:x][OFFSET: -12 ]\n[0:00.5][00:00:00.05][000:00.005]t",
		"[offset:99999999999999999999]\n[99999999999999999999:00]a\n[2562047788015:12:55.807]b\n[00:01]c",
		"[00:10][Chorus][00:20]x\n[00:10][00:75][00:20]x\n[ar:A][00:01]one [00:20]two\n[ti:Song [live]]",
		"[00:01]a<<00:02.00>>b<c<00:03>d<00:<00:02.00>03.00>e< 00:01 ><>\n",
		"[00:01]caf\xe9\nna\xefve\xff\xfe\n\xff\xfe[\x000\x000\x00",
		" \t[00:01] \t a \t \n\xc2\xa0[00:02]\xe3\x80\x80b\n[00:03]\x00\n",
		"[00:01:23]a\n[00:01:75]b\n[00:60:00]c\n[1:2:03]d\n[00:5]e\n[00:01.]f\n[00:01.2345]g\n[-1:00]h\n",
		"[t\xc4\xb0:T]\n[off\xc5\xbfet:500]\n[LENGTH:03:20]\n[bY:]\n[al:a:b]\n[artist:A]\n[ar :A]\n[ar]\n",
		"[00:02][00:01]a\r\r\n[00:01][00:02]b\n\r[offset:1500]",
	} {
		seeds = append(seeds, []byte(s))
	}
	return seeds
}

// FuzzParseLRC: Parse never panics, whatever the bytes; the times are never
// negative and never go back; lyrics that are not synced have no time and
// no empty line, synced ones a time on every line; a text is valid UTF-8
// on one line without white space around it; there are at most maxLines
// lines; the answer is the one of the model, does not depend on how the
// lines end, and a byte order mark before the file does not change it.
func FuzzParseLRC(f *testing.F) {
	for _, seed := range lrcSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		saved := slices.Clone(data)
		got := Parse(data)
		if !bytes.Equal(data, saved) {
			t.Fatal("Parse changed its input")
		}

		if got.Lines == nil {
			t.Fatal("Lines is nil")
		}
		if len(got.Lines) > maxLines {
			t.Fatalf("%d lines", len(got.Lines))
		}
		if got.Synced && len(got.Lines) == 0 {
			t.Fatal("synced lyrics without a line")
		}
		last := int64(0)
		for i, l := range got.Lines {
			switch {
			case !got.Synced && l.TimeMS != nil:
				t.Fatalf("line %d has the time %d in lyrics that are not synced", i, *l.TimeMS)
			case !got.Synced && l.Text == "":
				t.Fatalf("line %d is empty in lyrics that are not synced", i)
			case got.Synced && l.TimeMS == nil:
				t.Fatalf("line %d has no time in synced lyrics", i)
			}
			if l.TimeMS != nil {
				if *l.TimeMS < 0 {
					t.Fatalf("line %d has the negative time %d", i, *l.TimeMS)
				}
				if *l.TimeMS < last {
					t.Fatalf("line %d has the time %d after %d", i, *l.TimeMS, last)
				}
				last = *l.TimeMS
			}
			if !utf8.ValidString(l.Text) {
				t.Fatalf("the text of line %d is not UTF-8: %q", i, l.Text)
			}
			if strings.ContainsAny(l.Text, "\r\n") {
				t.Fatalf("the text of line %d has a line ending: %q", i, l.Text)
			}
			if strings.TrimSpace(l.Text) != l.Text {
				t.Fatalf("the text of line %d has white space around it: %q", i, l.Text)
			}
		}

		compareWithModel(t, data)

		same := func(what string, other []byte) {
			t.Helper()
			again := Parse(other)
			if again.Synced != got.Synced || !slices.Equal(flat(again), flat(got)) {
				t.Fatalf("%s gives another answer for %q\n got: %s\nthen: %s",
					what, data, show(got.Synced, flat(got)), show(again.Synced, flat(again)))
			}
		}
		same("a second reading", data)
		// An empty line is no line, so the three endings are one.
		same("CR for LF", bytes.ReplaceAll(data, []byte("\n"), []byte("\r")))
		same("LF for CR", bytes.ReplaceAll(data, []byte("\r"), []byte("\n")))
		same("CRLF for LF", bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")))
		same("a line ending at the end", append(slices.Clone(data), '\n'))
		if !bytes.HasPrefix(data, []byte(bom)) {
			same("a byte order mark", append([]byte(bom), data...))
		}
	})
}
