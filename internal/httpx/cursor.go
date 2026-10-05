package httpx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// codeInvalidCursor is the 400 of an `after` that is not a cursor of the
// list it is sent to (DESIGN.md §8.4).
const codeInvalidCursor = "invalid_cursor"

// The cursors of the paginated lists (DESIGN.md §8.5): base64url, without
// padding, of the JSON {"s": sort, "o": order, "k": [keys...]}, where the
// keys are the values of the complete order of the list for the last row of
// a page. A cursor is not signed and not secret: it says only where a page
// ends. It is input from the client, and is decoded strictly: a cursor is
// accepted only if it is exactly the one EncodeCursor writes for the values
// it holds, so there is one spelling of each cursor and nothing else gets
// through, whatever its JSON or its base64 look like.

// CursorKind is the type of one value of a cursor.
type CursorKind int

const (
	// CursorBytes is a sort key (a blob of the database), written as
	// standard base64 in the JSON.
	CursorBytes CursorKind = iota
	// CursorInt is an integer key.
	CursorInt
	// CursorID is an id: a UUID in its canonical form.
	CursorID
)

// CursorKey is one value of a cursor.
type CursorKey struct {
	kind  CursorKind
	bytes []byte
	n     int64
	id    string
}

// BytesKey is a value of kind CursorBytes.
func BytesKey(b []byte) CursorKey { return CursorKey{kind: CursorBytes, bytes: b} }

// IntKey is a value of kind CursorInt.
func IntKey(n int64) CursorKey { return CursorKey{kind: CursorInt, n: n} }

// IDKey is a value of kind CursorID.
func IDKey(id string) CursorKey { return CursorKey{kind: CursorID, id: id} }

// Bytes is the value of a key of kind CursorBytes.
func (k CursorKey) Bytes() []byte { return k.bytes }

// Int is the value of a key of kind CursorInt.
func (k CursorKey) Int() int64 { return k.n }

// ID is the value of a key of kind CursorID.
func (k CursorKey) ID() string { return k.id }

// cursorJSON is what a cursor holds. The order of the fields is the order
// in which they are written.
type cursorJSON struct {
	Sort  string            `json:"s"`
	Order string            `json:"o"`
	Keys  []json.RawMessage `json:"k"`
}

// EncodeCursor returns the cursor of the row whose key is keys, in a list
// ordered by sort and order. An id that is not a canonical UUID is an error:
// the cursor could not be read back.
func EncodeCursor(sort, order string, keys ...CursorKey) (string, error) {
	c := cursorJSON{Sort: sort, Order: order, Keys: make([]json.RawMessage, 0, len(keys))}
	for i, k := range keys {
		var v any
		switch k.kind {
		case CursorBytes:
			v = base64.StdEncoding.EncodeToString(k.bytes)
		case CursorInt:
			v = k.n
		case CursorID:
			if !canonicalUUID(k.id) {
				return "", fmt.Errorf("httpx: the key %d of a cursor is not a canonical id", i)
			}
			v = k.id
		default:
			return "", fmt.Errorf("httpx: the key %d of a cursor has no kind", i)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("httpx: encoding a cursor: %w", err)
		}
		c.Keys = append(c.Keys, b)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("httpx: encoding a cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor reads a cursor that EncodeCursor wrote for a list ordered
// by sort and order, whose keys are of the kinds given, in that order. Any
// other cursor, of another list, sort or order, or of no list at all, is
// 400 invalid_cursor. It never panics, whatever s is.
func DecodeCursor(s, sort, order string, kinds ...CursorKind) ([]CursorKey, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, InvalidCursor()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c cursorJSON
	if err := dec.Decode(&c); err != nil || c.Sort != sort || c.Order != order || len(c.Keys) != len(kinds) {
		return nil, InvalidCursor()
	}
	keys := make([]CursorKey, len(kinds))
	for i, kind := range kinds {
		k, ok := decodeKey(c.Keys[i], kind)
		if !ok {
			return nil, InvalidCursor()
		}
		keys[i] = k
	}
	// One spelling: what is left over after the value, white space, another
	// order of the fields, a key twice, an escape, a number written another
	// way all make another string than the one the values encode to.
	again, err := EncodeCursor(sort, order, keys...)
	if err != nil || again != s {
		return nil, InvalidCursor()
	}
	return keys, nil
}

// decodeKey reads one value of a cursor as kind.
func decodeKey(raw json.RawMessage, kind CursorKind) (CursorKey, bool) {
	switch kind {
	case CursorBytes:
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return CursorKey{}, false
		}
		b, err := base64.StdEncoding.Strict().DecodeString(text)
		if err != nil {
			return CursorKey{}, false
		}
		return BytesKey(b), true
	case CursorInt:
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return CursorKey{}, false
		}
		return IntKey(n), true
	case CursorID:
		var text string
		if json.Unmarshal(raw, &text) != nil || !canonicalUUID(text) {
			return CursorKey{}, false
		}
		return IDKey(text), true
	default:
		return CursorKey{}, false
	}
}

// canonicalUUID tells whether s is a UUID in the one form the API uses:
// lowercase, with hyphens (D19).
func canonicalUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s
}

// InvalidCursor is the 400 of a cursor that is not one of the list it is
// sent to. It says nothing of the cursor.
func InvalidCursor() *Error {
	return &Error{Status: http.StatusBadRequest, Code: codeInvalidCursor,
		Message: "The cursor is not one of this list, or belongs to another sort or order."}
}
