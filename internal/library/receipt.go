package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ReceiptName is the receipt MusicLib writes at the root of every album
// folder (DESIGN.md §4.2).
const ReceiptName = ".musiclib.json"

// MaxReceiptBytes is the longest receipt that is read (§4.2). It is
// MusicLib's own limit.
const MaxReceiptBytes = 16 << 20

// receiptSchemaVersion is the only schema_version this code understands.
const receiptSchemaVersion = 1

// Receipt is the content of a .musiclib.json of schema version 1 (§4.2). It
// attests the files of an album; it holds no name, title or timestamp.
type Receipt struct {
	// AlbumID is the identity of the album, for ever: a lowercase UUID.
	AlbumID string
	// BuildID is new at every build of the album folder.
	BuildID string
	// AlbumRevision rises with every change of the album's output.
	AlbumRevision int64
	RenderVersion string
	// Files are all the files of the album folder except the receipt, in
	// strictly ascending order of the bytes of Path: no path twice.
	Files []ReceiptFile
}

// ReceiptFile is one file of the album folder.
type ReceiptFile struct {
	// Path is relative to the album folder, with "/" as the separator.
	Path string
	Size int64
	// SHA256 is the lowercase hex SHA-256 of the file.
	SHA256 string
}

// ReceiptHash is the receipt_hash of the bytes of a receipt file: their
// SHA-256 in lowercase hex (§4.2). It changes at every build of the album,
// because the receipt holds the build_id.
func ReceiptHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ParseReceipt reads the bytes of a receipt. The error is an *Error:
// CodeReceiptSchemaUnsupported for a JSON object whose schema_version is an
// integer other than 1, whatever else it holds, and CodeReceiptInvalid for
// anything else that is refused.
//
// The receipt is not a public interface of MusicLib, so the reading is
// strict where a mistake would change what is indexed, and tolerant where
// MusicLib may add something within version 1:
//
//   - the bytes are one JSON object in valid UTF-8, and nothing follows it
//     but white space. The order of the keys and the white space are free;
//   - unknown fields are ignored, at the top and in a file. A known field is
//     matched by its exact name, and must not appear twice;
//   - album_id and build_id are UUIDs in the lowercase 8-4-4-4-12 form, and
//     not the nil UUID; album_revision is an integer above zero;
//     render_version is a string that is not empty;
//   - every file has a path that validRelPath accepts, a size that is an
//     integer not below zero and a SHA-256 of 64 lowercase hex digits;
//   - the files are in strictly ascending order of the bytes of their
//     paths, as MusicLib writes them: no path is listed twice;
//   - an integer is written without fraction or exponent, and a string
//     holds no escaped lone surrogate (which would silently become U+FFFD).
func ParseReceipt(data []byte) (Receipt, error) {
	r, err := parseReceipt(data)
	if err != nil {
		if Code(err) != "" {
			return Receipt{}, err
		}
		return Receipt{}, &Error{Code: CodeReceiptInvalid, Msg: "the receipt is not valid: " + err.Error()}
	}
	return r, nil
}

// parseReceipt is ParseReceipt; an error that is not an *Error is a reason
// why the receipt is not valid.
func parseReceipt(data []byte) (Receipt, error) {
	if !utf8.Valid(data) {
		return Receipt{}, errors.New("it is not valid UTF-8")
	}
	var schemaVersion, albumID, buildID, albumRevision, renderVersion, files json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	err := members(dec, func(key string, value json.RawMessage) error {
		switch key {
		case "schema_version":
			return setOnce(&schemaVersion, key, value)
		case "album_id":
			return setOnce(&albumID, key, value)
		case "build_id":
			return setOnce(&buildID, key, value)
		case "album_revision":
			return setOnce(&albumRevision, key, value)
		case "render_version":
			return setOnce(&renderVersion, key, value)
		case "files":
			return setOnce(&files, key, value)
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Receipt{}, errors.New("there is data after the JSON object")
	}

	version, ok := integer(schemaVersion)
	if !ok {
		return Receipt{}, errors.New("schema_version is missing or is not an integer")
	}
	// Before any other field: another version may give them another shape.
	if version != receiptSchemaVersion {
		return Receipt{}, &Error{Code: CodeReceiptSchemaUnsupported,
			Msg: fmt.Sprintf("the receipt has schema_version %d, and only %d is supported", version, receiptSchemaVersion)}
	}

	var r Receipt
	if r.AlbumID, ok = text(albumID); !ok || !validUUID(r.AlbumID) {
		return Receipt{}, errors.New("album_id is missing or is not a lowercase UUID")
	}
	if r.BuildID, ok = text(buildID); !ok || !validUUID(r.BuildID) {
		return Receipt{}, errors.New("build_id is missing or is not a lowercase UUID")
	}
	if r.AlbumRevision, ok = integer(albumRevision); !ok || r.AlbumRevision <= 0 {
		return Receipt{}, errors.New("album_revision is missing or is not an integer above zero")
	}
	if r.RenderVersion, ok = text(renderVersion); !ok || r.RenderVersion == "" {
		return Receipt{}, errors.New("render_version is missing or is not a string with a value")
	}
	if r.Files, err = parseFiles(files); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

// parseFiles reads the value of "files"; nil when the field is missing.
func parseFiles(raw json.RawMessage) ([]ReceiptFile, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errors.New("files is missing or is not an array")
	}
	// The elements are read one at a time, and the first that is refused
	// ends the reading: a receipt of millions of tiny elements costs
	// nothing.
	files := []ReceiptFile{}
	for dec.More() {
		f, err := parseFile(dec)
		if err != nil {
			return nil, fmt.Errorf("files[%d]: %w", len(files), err)
		}
		if n := len(files); n > 0 && files[n-1].Path >= f.Path {
			return nil, fmt.Errorf("files[%d]: its path is not after the path before it, in byte order", n)
		}
		files = append(files, f)
	}
	return files, nil
}

// parseFile reads the element of "files" that dec is at.
func parseFile(dec *json.Decoder) (ReceiptFile, error) {
	var relativePath, size, sum json.RawMessage
	err := members(dec, func(key string, value json.RawMessage) error {
		switch key {
		case "relative_path":
			return setOnce(&relativePath, key, value)
		case "size":
			return setOnce(&size, key, value)
		case "sha256":
			return setOnce(&sum, key, value)
		}
		return nil
	})
	if err != nil {
		return ReceiptFile{}, err
	}
	var f ReceiptFile
	var ok bool
	if f.Path, ok = text(relativePath); !ok || !validRelPath(f.Path) {
		return ReceiptFile{}, errors.New("relative_path is missing or is not a valid relative path")
	}
	if f.Size, ok = integer(size); !ok || f.Size < 0 {
		return ReceiptFile{}, errors.New("size is missing or is not an integer from zero up")
	}
	if f.SHA256, ok = text(sum); !ok || !validSHA256(f.SHA256) {
		return ReceiptFile{}, errors.New("sha256 is missing or is not 64 lowercase hex digits")
	}
	return f, nil
}

// members reads the JSON object that dec is at, and calls member with the
// name and the value of each of its members, in their order. encoding/json
// alone would match the names without their case and keep the last of two
// members with one name.
func members(dec *json.Decoder, member func(key string, value json.RawMessage) error) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("it is not JSON: %w", err)
	}
	if tok != json.Delim('{') {
		return errors.New("a JSON object is expected")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("it is not JSON: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("a member of a JSON object has no name")
		}
		// A new value for each member: decoding into a RawMessage reuses
		// its memory.
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return fmt.Errorf("it is not JSON: %w", err)
		}
		if err := member(key, value); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return fmt.Errorf("it is not JSON: %w", err)
	}
	return nil
}

// setOnce keeps the value of a known field, and refuses its second one.
func setOnce(field *json.RawMessage, key string, value json.RawMessage) error {
	if *field != nil {
		return fmt.Errorf("the field %q appears twice", key)
	}
	*field = value
	return nil
}

// integer reads raw as a JSON integer in its plain decimal form; false for
// a missing field, for another type, and for a number with a fraction, an
// exponent, a value beyond int64, or written as "-0".
func integer(raw json.RawMessage) (int64, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == string(raw)
}

// text reads raw as a JSON string; false for a missing field, for another
// type, and for a string that escapes a lone surrogate.
func text(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' || escapesLoneSurrogate(raw) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// escapesLoneSurrogate reports whether the JSON string raw holds a \uXXXX
// escape of half a UTF-16 surrogate pair without its other half.
// encoding/json decodes it as U+FFFD without an error: the string would
// not be the one the receipt holds, and a path would name another file.
func escapesLoneSurrogate(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++ // the escaped character: in `\\u` the "u" starts no escape
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		high := escapedUnit(raw[i+1:])
		if !utf16.IsSurrogate(high) {
			continue
		}
		low := rune(-1)
		if rest := raw[min(i+5, len(raw)):]; len(rest) >= 2 && rest[0] == '\\' && rest[1] == 'u' {
			low = escapedUnit(rest[2:])
		}
		if utf16.DecodeRune(high, low) == utf8.RuneError {
			return true
		}
		i += 10 // the rest of the pair: XXXX\uXXXX
	}
	return false
}

// escapedUnit is the UTF-16 code unit that the four hex digits at the start
// of b write, or -1.
func escapedUnit(b []byte) rune {
	if len(b) < 4 {
		return -1
	}
	n, err := strconv.ParseUint(string(b[:4]), 16, 16)
	if err != nil {
		return -1
	}
	return rune(n)
}

// validUUID reports whether s is a UUID as MusicLib and Vibrance write
// them (DESIGN.md D19): the lowercase 8-4-4-4-12 form, and not the nil
// UUID, which identifies nothing.
func validUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

// validSHA256 reports whether s is a SHA-256 in lowercase hex.
func validSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
