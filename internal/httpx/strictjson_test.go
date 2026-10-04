package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// The messages CheckJSON can give: each names a rule and nothing of the
// body.
var strictJSONMessages = []string{
	"The request body is not valid UTF-8.",
	"The request body is not one valid JSON value.",
	"The request body escapes a lone UTF-16 surrogate.",
	"The request body has the same key twice in one object.",
	"The request body is nested too deep.",
}

func nested(open, shut string, depth int) string {
	return strings.Repeat(open, depth) + strings.Repeat(shut, depth)
}

func TestCheckJSON(t *testing.T) {
	const (
		notUTF8   = "The request body is not valid UTF-8."
		notJSON   = "The request body is not one valid JSON value."
		surrogate = "The request body escapes a lone UTF-16 surrogate."
		twice     = "The request body has the same key twice in one object."
		deep      = "The request body is nested too deep."
	)
	for _, tc := range []struct {
		name string
		body string
		want string // the message, or "" when the body is accepted
	}{
		// Accepted.
		{"an object", `{"a":1,"b":"x","c":null,"d":true,"e":[1,2],"f":{"a":1}}`, ""},
		{"an empty object", `{}`, ""},
		{"an array", `[1,"a",{"a":1},{"a":2}]`, ""},
		{"a string", `"a"`, ""},
		{"a number", `-1.5e3`, ""},
		{"null", `null`, ""},
		{"white space around", " \t\r\n{\"a\":1}\r\n ", ""},
		{"the same key in two objects", `{"a":{"k":1},"b":{"k":2},"k":3}`, ""},
		{"the same key in two items of an array", `[{"k":1},{"k":2}]`, ""},
		{"keys that differ by case", `{"a":1,"A":2}`, ""}, // the validator refuses the unknown one
		{"a key and a value that are equal", `{"a":"a","b":"a"}`, ""},
		{"a string value equal to an earlier key, in an array", `{"a":["a","a"]}`, ""},
		{"an empty key once", `{"":1}`, ""},
		{"a surrogate pair", `{"a":"\ud83c\udfb5"}`, ""},
		{"a surrogate pair in upper case", `"\uD83C\uDFB5"`, ""},
		{"an escaped backslash before a u", `"\\ud800"`, ""},
		{"an escaped quote", `{"a\"b":1,"a":2}`, ""},
		{"U+FFFD itself", "\"\ufffd \\ufffd\"", ""},
		{"non-ASCII text", `{"title":"Beyoncé — 夜"}`, ""},
		{"the deepest nesting allowed", nested("[", "]", maxJSONDepth), ""},
		{"the deepest nesting allowed, objects", strings.Repeat(`{"a":`, maxJSONDepth-1) + `{}` + strings.Repeat(`}`, maxJSONDepth-1), ""},

		// Refused: duplicate keys, at every level.
		{"a duplicate key", `{"a":1,"a":2}`, twice},
		{"a duplicate key with equal values", `{"a":1,"a":1}`, twice},
		{"a duplicate key far apart", `{"a":1,"b":2,"c":3,"a":4}`, twice},
		{"a duplicate key in a nested object", `{"x":{"a":1,"a":2}}`, twice},
		{"a duplicate key two levels down", `{"x":{"y":{"a":1,"a":2}}}`, twice},
		{"a duplicate key in an object of an array", `{"x":[{"a":1},{"a":1,"a":2}]}`, twice},
		{"a duplicate key in an array at the top", `[{"a":1,"a":2}]`, twice},
		{"a duplicate key after a nested object", `{"a":{"b":1},"a":2}`, twice},
		{"a duplicate key after a nested array", `{"a":[1,2],"c":1,"a":2}`, twice},
		{"a duplicate key written with an escape", `{"a":1,"\u0061":2}`, twice},
		{"a duplicate key written with two escapes", `{"\u0061":1,"\u0061":2}`, twice},
		{"a duplicate empty key", `{"":1,"":2}`, twice},
		{"a duplicate key whose values are objects", `{"a":{},"a":{}}`, twice},
		{"the password twice", `{"username":"anna","password":"one","password":"two"}`, twice},

		// Refused: not exactly one value.
		{"nothing", ``, notJSON},
		{"only white space", " \n", notJSON},
		{"two objects", `{"a":1}{"a":1}`, notJSON},
		{"two objects on two lines", "{\"a\":1}\n{\"b\":2}", notJSON},
		{"two scalars", `1 2`, notJSON},
		{"a value and garbage", `{"a":1} x`, notJSON},
		{"a value and a comma", `{"a":1},`, notJSON},
		{"an open object", `{"a":1`, notJSON},
		{"an open string", `{"a":"x`, notJSON},
		{"a trailing comma", `{"a":1,}`, notJSON},
		{"a missing colon", `{"a" 1}`, notJSON},
		{"a missing comma", `[1 2]`, notJSON},
		{"single quotes", `{'a':1}`, notJSON},
		{"a bare word", `{"a":yes}`, notJSON},
		{"a comment", `{"a":1} // x`, notJSON},
		{"a byte order mark", "\ufeff{\"a\":1}", notJSON},
		{"a NUL after the value", "{}\x00", notJSON},
		{"a control character in a string", "\"a\nb\"", notJSON},
		{"a wrong escape", `"\x41"`, notJSON},
		{"a short \\u escape", `"\u12"`, notJSON},
		{"a number with a leading zero", `01`, notJSON},
		{"NaN", `NaN`, notJSON},

		// Refused: what encoding/json would replace without a word.
		{"bytes that are not UTF-8", "{\"a\":\"\xff\"}", notUTF8},
		{"a truncated UTF-8 sequence", "\"\xe2\x82\"", notUTF8},
		{"an encoded surrogate", "\"\xed\xa0\x80\"", notUTF8},
		{"a lone high surrogate", `{"a":"\ud83c"}`, surrogate},
		{"a lone low surrogate", `{"a":"\udfb5"}`, surrogate},
		{"a high surrogate at the end of a string", `"x\ud800"`, surrogate},
		{"a high surrogate before a character", `"\ud800x"`, surrogate},
		{"a high surrogate before another escape", `"\ud800\n"`, surrogate},
		{"two high surrogates", `"\ud83c\ud83c"`, surrogate},
		{"a pair in the wrong order", `"\udfb5\ud83c"`, surrogate},
		{"a lone surrogate in a key", `{"\ud800":1}`, surrogate},
		{"a lone surrogate in upper case", `"\uD800"`, surrogate},
		{"a lone surrogate after an escaped backslash", `"\\\ud800"`, surrogate},

		// Refused: too deep.
		{"one array too deep", nested("[", "]", maxJSONDepth+1), deep},
		{"one object too deep", strings.Repeat(`{"a":`, maxJSONDepth) + `{}` + strings.Repeat(`}`, maxJSONDepth), deep},
		{"ten thousand arrays", nested("[", "]", 10000), deep},
		{"deeper than encoding/json reads", nested("[", "]", 10001), notJSON},
	} {
		err := CheckJSON([]byte(tc.body))
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: refused: %v", tc.name, err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: CheckJSON = %v, want the refusal %q", tc.name, err, tc.want)
			continue
		}
		if e.Status != http.StatusBadRequest || e.Code != "invalid_request" || e.Message != tc.want || e.Details != nil {
			t.Errorf("%s: %+v, want 400 invalid_request %q", tc.name, e, tc.want)
		}
	}
}

// A megabyte of each hostile shape is checked without the time or the
// memory growing with the square of its size.
func TestCheckJSONOnLargeBodies(t *testing.T) {
	var keys strings.Builder
	keys.WriteString("{")
	for i := 0; keys.Len() < MaxBodyBytes-32; i++ {
		if i > 0 {
			keys.WriteString(",")
		}
		keys.WriteString(`"k`)
		keys.WriteString(strings.Repeat("x", i%7))
		keys.WriteString(string(rune('a' + i%26)))
		keys.WriteString(itoa(i))
		keys.WriteString(`":1`)
	}
	keys.WriteString("}")
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"many distinct keys":   {keys.String(), true},
		"one long string":      {`"` + strings.Repeat("a", MaxBodyBytes-2) + `"`, true},
		"many escapes":         {`"` + strings.Repeat(`\ud83c\udfb5`, (MaxBodyBytes-2)/12) + `"`, true},
		"a long array":         {"[" + strings.Repeat("1,", (MaxBodyBytes-3)/2) + "1]", true},
		"only open brackets":   {strings.Repeat("[", MaxBodyBytes), false},
		"only open braces":     {strings.Repeat(`{"a":`, MaxBodyBytes/5), false},
		"brackets, well shut":  {nested("[", "]", MaxBodyBytes/2), false},
		"a long run of spaces": {strings.Repeat(" ", MaxBodyBytes-2) + "{}", true},
	} {
		if err := CheckJSON([]byte(tc.body)); (err == nil) != tc.ok {
			t.Errorf("%s: CheckJSON = %v, want accepted = %v", name, err, tc.ok)
		}
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// reference is a second writing of the rules of CheckJSON, with other
// means: the decoder of encoding/json instead of its tokenizer, and what the
// decoded strings hold instead of a walk of the escapes.
//
// ok is whether b follows every rule but the one on surrogates. replaced is
// whether decoding b made a U+FFFD that was not in it, which, b being valid
// UTF-8, only a lone surrogate does; it is known only when sure, that is
// when b has no U+FFFD of its own, written or escaped.
func reference(b []byte) (ok, replaced, sure bool) {
	if !utf8.Valid(b) {
		return false, false, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return false, false, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return false, false, false // a second value, or garbage
	}
	if !referenceValue(raw, 0) {
		return false, false, false
	}
	// Numbers stay text: one that fits no float (1E700) is still JSON, and
	// is refused later, by whoever needs its value.
	var v any
	values := json.NewDecoder(bytes.NewReader(raw))
	values.UseNumber()
	if err := values.Decode(&v); err != nil {
		return false, false, false
	}
	sure = !bytes.Contains(bytes.ToLower(b), []byte("fffd")) && !bytes.Contains(b, []byte("\ufffd"))
	return true, hasReplacement(v), sure
}

// referenceValue reports whether raw, one valid JSON value inside depth
// arrays and objects, has no duplicate key and is not nested too deep.
func referenceValue(raw json.RawMessage, depth int) bool {
	switch bytes.TrimLeft(raw, " \t\r\n")[0] {
	case '[':
		if depth == maxJSONDepth {
			return false
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return false
		}
		for _, item := range items {
			if !referenceValue(item, depth+1) {
				return false
			}
		}
	case '{':
		if depth == maxJSONDepth {
			return false
		}
		// The map has each key once; the decoder is asked how many members
		// the object has.
		var byKey map[string]json.RawMessage
		if json.Unmarshal(raw, &byKey) != nil {
			return false
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		if _, err := dec.Token(); err != nil {
			return false
		}
		members := 0
		for dec.More() {
			if _, err := dec.Token(); err != nil { // the key
				return false
			}
			var value json.RawMessage
			if dec.Decode(&value) != nil {
				return false
			}
			members++
			if !referenceValue(value, depth+1) {
				return false
			}
		}
		return members == len(byKey)
	}
	return true
}

func hasReplacement(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.ContainsRune(v, utf8.RuneError)
	case []any:
		for _, item := range v {
			if hasReplacement(item) {
				return true
			}
		}
	case map[string]any:
		for key, value := range v {
			if strings.ContainsRune(key, utf8.RuneError) || hasReplacement(value) {
				return true
			}
		}
	}
	return false
}

// The table of TestCheckJSON agrees with the reference too: a wrong
// reference would make the fuzz target prove nothing.
func TestCheckJSONReference(t *testing.T) {
	for _, tc := range []struct {
		body                string
		ok, replaced, known bool
	}{
		{`{"a":1}`, true, false, true},
		{`{"a":1,"a":2}`, false, false, false},
		{`{"x":[{"a":1,"\u0061":2}]}`, false, false, false},
		{`{"a":1}{"a":1}`, false, false, false},
		{`{"a":1} x`, false, false, false},
		{"\"\xff\"", false, false, false},
		{`"\ud800"`, true, true, true},
		{`"\ud83c\udfb5"`, true, false, true},
		{`"\ufffd"`, true, true, false},
		{nested("[", "]", maxJSONDepth), true, false, true},
		{nested("[", "]", maxJSONDepth+1), false, false, false},
	} {
		ok, replaced, sure := reference([]byte(tc.body))
		if ok != tc.ok || replaced != tc.replaced || sure != tc.known {
			t.Errorf("reference(%q) = %v, %v, %v; want %v, %v, %v", tc.body, ok, replaced, sure, tc.ok, tc.replaced, tc.known)
		}
	}
}

// FuzzStrictJSON: CheckJSON never panics, refuses with one of its fixed
// messages, and agrees with the reference. A body it accepts is one that
// encoding/json decodes without changing a character and without dropping a
// member.
func FuzzStrictJSON(f *testing.F) {
	for _, seed := range []string{
		``, ` `, `{}`, `[]`, `null`, `1`, `"a"`, `{"a":1}`, `{"a":1,"a":2}`, `{"a":1,"\u0061":2}`,
		`{"x":{"a":1,"a":2}}`, `[{"a":1},{"a":1,"a":2}]`, `{"a":{"b":1},"a":2}`, `{"a":1}{"a":1}`, `{"a":1} x`,
		`{"a":"\ud83c\udfb5"}`, `{"a":"\ud83c"}`, `"\udfb5"`, `"\\ud800"`, `"\\\ud800"`, `{"\ud800":1}`, `"\ud800\ud800"`,
		"\"\xff\"", "\ufeff{}", "\"\ufffd\"", `"\ufffd"`, `"\uFFFD\ud800"`, `{"a":[1,2,{"b":null}],"c":true}`,
		`{"username":"anna","password":"correct horse","password":"other"}`, `{"a":1,"A":2}`, `{"":1,"":2}`,
		nested("[", "]", maxJSONDepth), nested("[", "]", maxJSONDepth+1), strings.Repeat(`{"a":`, 40) + `1` + strings.Repeat(`}`, 40),
		`[1 2]`, `{"a" 1}`, `{"a":1,}`, `01`, `"\u12"`, `"a\"b"`, `{"a\"b":1,"a":2}`, "{}\x00", `[[],[[]],{"a":[{"a":{}}]}]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		err := CheckJSON(b)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Status != http.StatusBadRequest || e.Code != "invalid_request" || e.Details != nil {
				t.Fatalf("CheckJSON(%q) = %v, want a 400 invalid_request", b, err)
			}
			known := false
			for _, m := range strictJSONMessages {
				known = known || e.Message == m
			}
			if !known {
				t.Fatalf("CheckJSON(%q): unexpected message %q", b, e.Message)
			}
		}

		ok, replaced, sure := reference(b)
		accepted := err == nil
		switch {
		case !ok && accepted:
			t.Fatalf("CheckJSON accepts %q, which the reference refuses", b)
		case ok && sure && accepted == replaced:
			t.Fatalf("CheckJSON(%q) = %v; the reference: a character replaced in decoding = %v", b, err, replaced)
		case ok && !sure && !accepted && !replaced:
			// Without a replaced character there is no lone surrogate.
			t.Fatalf("CheckJSON refuses %q (%v), which the reference accepts", b, err)
		}
		if accepted && !json.Valid(b) {
			t.Fatalf("CheckJSON accepts %q, which is not valid JSON", b)
		}
	})
}
