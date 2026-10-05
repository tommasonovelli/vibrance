package httpx

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"testing"
)

// The cursors of DESIGN.md §8.5: what EncodeCursor writes, DecodeCursor
// reads back exactly, and nothing else (T9).

const cursorID = "0199a5c0-7b1e-7c3a-9d2f-4b6a8c0e1f23"

var artistShape = []CursorKind{CursorBytes, CursorInt, CursorBytes, CursorID}

func mustEncode(t *testing.T, sort, order string, keys ...CursorKey) string {
	t.Helper()
	s, err := EncodeCursor(sort, order, keys...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// raw is the cursor of a JSON text, as EncodeCursor would wrap it.
func raw(json string) string { return base64.RawURLEncoding.EncodeToString([]byte(json)) }

func wantInvalidCursor(t *testing.T, where string, err error) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != http.StatusBadRequest || e.Code != "invalid_cursor" {
		t.Errorf("%s: %v, want 400 invalid_cursor", where, err)
	}
}

func sameKeys(a, b []CursorKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind || !bytes.Equal(a[i].bytes, b[i].bytes) || a[i].n != b[i].n || a[i].id != b[i].id {
			return false
		}
	}
	return true
}

// A cursor holds its sort, its order and its values, and gives them back.
func TestCursorRoundTrip(t *testing.T) {
	for _, keys := range [][]CursorKey{
		{BytesKey([]byte{0, 1, 0xff, '+', '/'}), IntKey(10000), BytesKey(nil), IDKey(cursorID)},
		{BytesKey([]byte{}), IntKey(math.MinInt64), BytesKey([]byte("é")), IDKey(cursorID)},
		{BytesKey(bytes.Repeat([]byte{0xfb}, 3000)), IntKey(math.MaxInt64), BytesKey([]byte{0}), IDKey(cursorID)},
	} {
		c := mustEncode(t, "artist", "desc", keys...)
		got, err := DecodeCursor(c, "artist", "desc", artistShape...)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if !sameKeys(got, keys) {
			t.Fatalf("%s: %+v, want %+v", c, got, keys)
		}
		for _, r := range c {
			if r == '=' || r == '+' || r == '/' {
				t.Fatalf("%s: not base64url without padding", c)
			}
		}
	}
	// What the JSON looks like, once.
	c := mustEncode(t, "added", "asc", IntKey(1727000000000), IDKey(cursorID))
	if want := raw(`{"s":"added","o":"asc","k":[1727000000000,"` + cursorID + `"]}`); c != want {
		t.Fatalf("cursor %s, want %s", c, want)
	}
}

// An id a cursor could not give back is not written.
func TestEncodeCursorRefusesAnIDItCannotRead(t *testing.T) {
	for _, id := range []string{"", "x", "0199A5C0-7B1E-7C3A-9D2F-4B6A8C0E1F23", "{" + cursorID + "}"} {
		if _, err := EncodeCursor("title", "asc", BytesKey(nil), IDKey(id)); err == nil {
			t.Errorf("an id %q was encoded", id)
		}
	}
	if _, err := EncodeCursor("title", "asc", CursorKey{}); err != nil {
		t.Errorf("the zero key is bytes: %v", err)
	}
}

// A cursor of another list, sort or order, a tampered one, and any spelling
// EncodeCursor would not write, are 400 invalid_cursor.
func TestDecodeCursorRefuses(t *testing.T) {
	good := mustEncode(t, "title", "asc", BytesKey([]byte{1, 2, 3}), IDKey(cursorID))
	if _, err := DecodeCursor(good, "title", "asc", CursorBytes, CursorID); err != nil {
		t.Fatalf("the good cursor: %v", err)
	}
	titleShape := []CursorKind{CursorBytes, CursorID}
	// A letter of standard base64 that base64url does not have.
	_, err := DecodeCursor(good[:4]+"+"+good[5:], "title", "asc", titleShape...)
	wantInvalidCursor(t, "standard base64", err)
	b64 := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	for name, c := range map[string]string{
		"empty":                    "",
		"not base64":               "!!!",
		"base64 with a newline":    good[:4] + "\n" + good[4:],
		"one character more":       good + "A",
		"one character less":       good[:len(good)-1],
		"not JSON":                 raw(`title,asc`),
		"JSON null":                raw(`null`),
		"an array":                 raw(`["title","asc"]`),
		"another sort":             raw(`{"s":"year","o":"asc","k":["` + b64 + `","` + cursorID + `"]}`),
		"another order":            raw(`{"s":"title","o":"desc","k":["` + b64 + `","` + cursorID + `"]}`),
		"the sort in upper case":   raw(`{"s":"TITLE","o":"asc","k":["` + b64 + `","` + cursorID + `"]}`),
		"one value less":           raw(`{"s":"title","o":"asc","k":["` + cursorID + `"]}`),
		"one value more":           raw(`{"s":"title","o":"asc","k":["` + b64 + `","` + cursorID + `",1]}`),
		"no values":                raw(`{"s":"title","o":"asc","k":null}`),
		"no field k":               raw(`{"s":"title","o":"asc"}`),
		"an unknown field":         raw(`{"s":"title","o":"asc","k":["` + b64 + `","` + cursorID + `"],"x":1}`),
		"a field twice":            raw(`{"s":"title","s":"title","o":"asc","k":["` + b64 + `","` + cursorID + `"]}`),
		"the fields in disorder":   raw(`{"o":"asc","s":"title","k":["` + b64 + `","` + cursorID + `"]}`),
		"white space":              raw(`{"s":"title", "o":"asc","k":["` + b64 + `","` + cursorID + `"]}`),
		"two JSON values":          raw(`{"s":"title","o":"asc","k":["` + b64 + `","` + cursorID + `"]}{}`),
		"a key that is a number":   raw(`{"s":"title","o":"asc","k":[1,"` + cursorID + `"]}`),
		"a key that is not base64": raw(`{"s":"title","o":"asc","k":["@@","` + cursorID + `"]}`),
		"a key with padding bits":  raw(`{"s":"title","o":"asc","k":["AQJ=","` + cursorID + `"]}`),
		"a key without padding":    raw(`{"s":"title","o":"asc","k":["AQ","` + cursorID + `"]}`),
		"an escaped key":           raw(`{"s":"title","o":"asc","k":["` + string(rune(0x5c)) + `u0041QID","` + cursorID + `"]}`),
		"padding added":            good + "=",
		"an id in upper case":      raw(`{"s":"title","o":"asc","k":["` + b64 + `","0199A5C0-7B1E-7C3A-9D2F-4B6A8C0E1F23"]}`),
		"an id without hyphens":    raw(`{"s":"title","o":"asc","k":["` + b64 + `","0199a5c07b1e7c3a9d2f4b6a8c0e1f23"]}`),
		"an id that is not a UUID": raw(`{"s":"title","o":"asc","k":["` + b64 + `","x' OR 1=1 --"]}`),
		"an id that is a number":   raw(`{"s":"title","o":"asc","k":["` + b64 + `",7]}`),
		"an id that is null":       raw(`{"s":"title","o":"asc","k":["` + b64 + `",null]}`),
		"invalid UTF-8":            raw("{\"s\":\"title\",\"o\":\"asc\",\"k\":[\"" + b64 + "\",\"" + cursorID + "\xff\"]}"),
	} {
		_, err := DecodeCursor(c, "title", "asc", titleShape...)
		wantInvalidCursor(t, name, err)
	}

	yearShape := []CursorKind{CursorInt, CursorBytes, CursorID}
	for name, k0 := range map[string]string{
		"a fraction":           `2.0`,
		"an exponent":          `2e3`,
		"a leading zero":       `02`,
		"a plus":               `+2`,
		"a string":             `"2"`,
		"over the int64 range": `9223372036854775808`,
		"null":                 `null`,
		"true":                 `true`,
		"negative zero":        `-0`,
	} {
		c := raw(`{"s":"year","o":"asc","k":[` + k0 + `,"` + b64 + `","` + cursorID + `"]}`)
		_, err := DecodeCursor(c, "year", "asc", yearShape...)
		wantInvalidCursor(t, "a year that is "+name, err)
	}
	if _, err := DecodeCursor(raw(`{"s":"year","o":"asc","k":[-2,"`+b64+`","`+cursorID+`"]}`), "year", "asc", yearShape...); err != nil {
		t.Errorf("a negative integer is a value: %v", err)
	}
	// A kind DecodeCursor does not know is no cursor either.
	_, err = DecodeCursor(good, "title", "asc", CursorBytes, CursorKind(9))
	wantInvalidCursor(t, "an unknown kind", err)
}

// The refusal says nothing of the cursor sent.
func TestInvalidCursorSaysNothingOfTheCursor(t *testing.T) {
	const secret = "x' OR 1=1 --"
	_, err := DecodeCursor(raw(`{"s":"title","o":"asc","k":["AQID","`+secret+`"]}`), "title", "asc", CursorBytes, CursorID)
	var e *Error
	if !errors.As(err, &e) || bytes.Contains([]byte(e.Message), []byte("OR")) || len(e.Details) != 0 {
		t.Fatalf("%+v", e)
	}
}

// FuzzCursor (DESIGN.md §12.5): DecodeCursor never panics and accepts a
// string only if EncodeCursor writes exactly that string for the values it
// read; and every cursor EncodeCursor writes is read back with its values.
func FuzzCursor(f *testing.F) {
	f.Add(mustEncodeF(f, "artist", "asc", BytesKey([]byte{1}), IntKey(1959), BytesKey([]byte{2}), IDKey(cursorID)), []byte{1, 2}, int64(1959), []byte(cursorID))
	f.Add(raw(`{"s":"artist","o":"asc","k":["AQ==",1,"Ag==","`+cursorID+`"]}`), []byte{}, int64(-1), []byte("x"))
	f.Add(raw(`{"s":"artist","o":"asc","k":["AQ==",1e0,"Ag==","`+cursorID+`"]}`), []byte(nil), int64(math.MaxInt64), []byte(""))
	f.Add("", []byte{0xff}, int64(0), []byte("0199A5C0-7B1E-7C3A-9D2F-4B6A8C0E1F23"))
	f.Fuzz(func(t *testing.T, s string, key []byte, n int64, id []byte) {
		if keys, err := DecodeCursor(s, "artist", "asc", artistShape...); err == nil {
			again, err := EncodeCursor("artist", "asc", keys...)
			if err != nil || again != s {
				t.Fatalf("%q was read, and its values encode to %q (%v)", s, again, err)
			}
		} else {
			wantInvalidCursor(t, s, err)
		}

		keys := []CursorKey{BytesKey(key), IntKey(n), BytesKey(id), IDKey(string(id))}
		c, err := EncodeCursor("artist", "asc", keys...)
		if err != nil {
			if canonicalUUID(string(id)) {
				t.Fatalf("encoding %+v: %v", keys, err)
			}
			return
		}
		got, err := DecodeCursor(c, "artist", "asc", artistShape...)
		if err != nil {
			t.Fatalf("%s, encoded from %+v: %v", c, keys, err)
		}
		if !sameKeys(got, keys) {
			t.Fatalf("%s: %+v, want %+v", c, got, keys)
		}
		if _, err := DecodeCursor(c, "artist", "desc", artistShape...); err == nil {
			t.Fatalf("%s was read with another order", c)
		}
	})
}

func mustEncodeF(f *testing.F, sort, order string, keys ...CursorKey) string {
	s, err := EncodeCursor(sort, order, keys...)
	if err != nil {
		f.Fatal(err)
	}
	return s
}
