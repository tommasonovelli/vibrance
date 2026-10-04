package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// maxJSONDepth is how deep the arrays and objects of a body may nest. The
// bodies of the API nest three levels; the limit keeps a body of a million
// brackets from costing a frame each.
const maxJSONDepth = 32

// CheckJSON checks that b is a JSON body the API can trust (DESIGN.md §8.1,
// T23), which encoding/json and the OpenAPI validator alone do not:
//
//   - valid UTF-8, and no \u escape that is a lone UTF-16 surrogate:
//     encoding/json would turn both into U+FFFD without a word, and two
//     different passwords would become the same one;
//   - exactly one JSON value, with nothing after it but white space;
//   - no object with the same key twice, at any depth: encoding/json keeps
//     the last one, and what the validator checked would not be what the
//     handler reads;
//   - at most maxJSONDepth levels.
//
// Keys are compared after unescaping, so "a" and "a" are the same key.
// Unknown keys and keys that differ only by case are the validator's: every
// request schema is closed. The error is a 400 invalid_request that says
// which rule was broken and nothing of the body.
func CheckJSON(b []byte) error {
	switch {
	case !utf8.Valid(b):
		return invalidBody("The request body is not valid UTF-8.")
	case !json.Valid(b):
		return invalidBody("The request body is not one valid JSON value.")
	case hasLoneSurrogate(b):
		return invalidBody("The request body escapes a lone UTF-16 surrogate.")
	}
	return checkKeys(b)
}

// checkKeys walks b, which is one valid JSON value, with the tokenizer of
// encoding/json, and refuses a key that appears twice in its object and a
// value nested too deep.
func checkKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	// Numbers stay text: nothing here needs their value.
	dec.UseNumber()
	// One entry per open value: the keys seen so far in an object, nil for
	// an array.
	var open []map[string]struct{}
	// wantKey: the next string token is a key of the innermost object.
	wantKey := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// json.Valid said otherwise.
			return invalidBody("The request body is not one valid JSON value.")
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			if len(open) == maxJSONDepth {
				return invalidBody("The request body is nested too deep.")
			}
			var keys map[string]struct{}
			if tok == json.Delim('{') {
				keys = map[string]struct{}{}
			}
			open = append(open, keys)
			wantKey = keys != nil
			continue
		case json.Delim('}'), json.Delim(']'):
			open = open[:len(open)-1]
		default:
			if key, isString := tok.(string); isString && wantKey {
				keys := open[len(open)-1]
				if _, twice := keys[key]; twice {
					return invalidBody("The request body has the same key twice in one object.")
				}
				keys[key] = struct{}{}
				wantKey = false
				continue
			}
		}
		// A value has just ended: in an object the next token is a key.
		wantKey = len(open) > 0 && open[len(open)-1] != nil
	}
}

// hasLoneSurrogate reports whether a string of b, which is valid JSON, has a
// \u escape that is a high surrogate not followed by a low one, or a low one
// alone.
func hasLoneSurrogate(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !inString {
			inString = c == '"'
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if b[i+1] != 'u' {
				i++ // a one-character escape, \" and \\ included
				continue
			}
			r := hex4(b[i+2 : i+6])
			i += 5
			switch {
			case r >= 0xdc00 && r <= 0xdfff:
				return true
			case r >= 0xd800 && r <= 0xdbff:
				if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
					return true
				}
				if low := hex4(b[i+3 : i+7]); low < 0xdc00 || low > 0xdfff {
					return true
				}
				i += 6
			}
		}
	}
	return false
}

// hex4 decodes four hexadecimal digits, which the JSON syntax has checked.
func hex4(h []byte) rune {
	var r rune
	for _, c := range h {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		default:
			r |= rune(c-'A') + 10
		}
	}
	return r
}
