package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// receiptName is MusicLib's receipt, at the root of every album folder
// (DESIGN.md §4.2).
const receiptName = ".musiclib.json"

// receipt is the content of .musiclib.json (DESIGN.md §4.2).
type receipt struct {
	SchemaVersion int           `json:"schema_version"`
	AlbumID       string        `json:"album_id"`
	BuildID       string        `json:"build_id"`
	AlbumRevision int64         `json:"album_revision"`
	RenderVersion string        `json:"render_version"`
	Files         []receiptFile `json:"files"`
}

type receiptFile struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// parseReceipt decodes a receipt strictly: no unknown key, and the bytes
// must be exactly the canonical form that DESIGN.md §4.2 describes (one line
// of compact JSON, keys in the documented order, no trailing newline), which
// canonicalReceipt rebuilds. It also checks the values that Vibrance will
// validate (§4.2).
func parseReceipt(raw []byte) (receipt, error) {
	var r receipt
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return receipt{}, fmt.Errorf("the receipt does not decode: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return receipt{}, errors.New("the receipt has data after its JSON object")
	}
	switch {
	case r.SchemaVersion != 1:
		return receipt{}, fmt.Errorf("schema_version %d", r.SchemaVersion)
	case !uuidPattern.MatchString(r.AlbumID):
		return receipt{}, fmt.Errorf("album_id %q is not a lowercase UUID", r.AlbumID)
	case !uuidPattern.MatchString(r.BuildID):
		return receipt{}, fmt.Errorf("build_id %q is not a lowercase UUID", r.BuildID)
	case r.AlbumRevision <= 0:
		return receipt{}, fmt.Errorf("album_revision %d", r.AlbumRevision)
	case r.RenderVersion == "":
		return receipt{}, errors.New("render_version is empty")
	}
	for i, f := range r.Files {
		switch {
		case f.RelativePath == "" || !fs.ValidPath(f.RelativePath) || strings.Contains(f.RelativePath, `\`):
			return receipt{}, fmt.Errorf("files[%d]: invalid relative_path %q", i, f.RelativePath)
		case f.Size < 0:
			return receipt{}, fmt.Errorf("files[%d]: size %d", i, f.Size)
		case !hexPattern.MatchString(f.SHA256):
			return receipt{}, fmt.Errorf("files[%d]: sha256 %q", i, f.SHA256)
		case i > 0 && r.Files[i-1].RelativePath >= f.RelativePath:
			return receipt{}, fmt.Errorf("files[%d]: %q is not after %q in byte order", i, f.RelativePath, r.Files[i-1].RelativePath)
		}
	}
	if canon := canonicalReceipt(r); !bytes.Equal(canon, raw) {
		return receipt{}, fmt.Errorf("the receipt is not in the documented canonical form:\n got: %q\nwant: %q", raw, canon)
	}
	return r, nil
}

// canonicalReceipt is the one-line form of DESIGN.md §4.2: compact JSON,
// keys in the documented order, no trailing newline; strings escape only
// `"`, `\` and the control characters.
func canonicalReceipt(r receipt) []byte {
	var b bytes.Buffer
	b.WriteString(`{"schema_version":` + strconv.Itoa(r.SchemaVersion))
	b.WriteString(`,"album_id":` + jsonString(r.AlbumID))
	b.WriteString(`,"build_id":` + jsonString(r.BuildID))
	b.WriteString(`,"album_revision":` + strconv.FormatInt(r.AlbumRevision, 10))
	b.WriteString(`,"render_version":` + jsonString(r.RenderVersion))
	b.WriteString(`,"files":[`)
	for i, f := range r.Files {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"relative_path":` + jsonString(f.RelativePath) +
			`,"size":` + strconv.FormatInt(f.Size, 10) + `,"sha256":` + jsonString(f.SHA256) + `}`)
	}
	b.WriteString("]}")
	return b.Bytes()
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20:
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// albumDir is one album folder found on disk.
type albumDir struct {
	// Rel is "Artist/Album", relative to the library root.
	Rel     string
	Receipt receipt
	// Hash is the SHA-256 of the receipt's bytes (receipt_hash).
	Hash string
}

// scanLibrary lists the album folders of the library at root, two levels
// deep as Vibrance's scanner does (DESIGN.md §6.2), and parses each receipt.
// Folders and files whose names start with "." are skipped; a folder
// without a receipt, or with an invalid one, is an error.
func scanLibrary(root string) ([]albumDir, error) {
	artists, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("listing the library: %w", err)
	}
	var out []albumDir
	for _, a := range artists {
		if !a.IsDir() || strings.HasPrefix(a.Name(), ".") {
			continue
		}
		albums, err := os.ReadDir(filepath.Join(root, a.Name()))
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", a.Name(), err)
		}
		for _, al := range albums {
			if !al.IsDir() || strings.HasPrefix(al.Name(), ".") {
				continue
			}
			rel := a.Name() + "/" + al.Name()
			raw, err := os.ReadFile(filepath.Join(root, rel, receiptName))
			if err != nil {
				return nil, fmt.Errorf("reading the receipt of %s: %w", rel, err)
			}
			r, err := parseReceipt(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
			sum := sha256.Sum256(raw)
			out = append(out, albumDir{Rel: rel, Receipt: r, Hash: hex.EncodeToString(sum[:])})
		}
	}
	return out, nil
}

// checkAlbumFiles compares the receipt of the album folder dir with the
// folder's content: the receipt must not list itself, and every regular
// file of the folder except the receipt must be listed with its size and
// SHA-256, and nothing else. It returns the problems found, nil when there
// is none. Symbolic links and other special files are problems too.
func checkAlbumFiles(dir string, r receipt) ([]string, error) {
	onDisk := map[string]bool{}
	var problems []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			problems = append(problems, rel+": not a regular file")
		case rel != receiptName:
			onDisk[rel] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", dir, err)
	}
	for _, f := range r.Files {
		if path.Clean(f.RelativePath) == receiptName {
			problems = append(problems, "the receipt lists itself")
			continue
		}
		if !onDisk[f.RelativePath] {
			problems = append(problems, f.RelativePath+": listed, not on disk")
			continue
		}
		delete(onDisk, f.RelativePath)
		size, sum, err := fileSHA256(filepath.Join(dir, filepath.FromSlash(f.RelativePath)))
		if err != nil {
			return nil, err
		}
		if size != f.Size {
			problems = append(problems, fmt.Sprintf("%s: size %d on disk, %d in the receipt", f.RelativePath, size, f.Size))
		}
		if sum != f.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s on disk, %s in the receipt", f.RelativePath, sum, f.SHA256))
		}
	}
	extra := make([]string, 0, len(onDisk))
	for rel := range onDisk {
		extra = append(extra, rel)
	}
	slices.Sort(extra)
	for _, rel := range extra {
		problems = append(problems, rel+": on disk, not listed")
	}
	return problems, nil
}

// fileSHA256 returns the size and the hex SHA-256 of the file at p.
func fileSHA256(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", fmt.Errorf("opening %s: %w", p, err)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return 0, "", fmt.Errorf("reading %s: %w", p, err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// isAudio reports whether rel, a path of a receipt, is a track: an audio
// extension, outside Extras/ (DESIGN.md §6.3).
func isAudio(rel string) bool {
	return demuxerOf[strings.ToLower(path.Ext(rel))] != "" && !strings.HasPrefix(rel, "Extras/")
}

// trackSlot reads (disc, no) from a track path of MusicLib's layout,
// "[Disc N/]NN - Title.ext" (DESIGN.md §4.1). The spike uses it only to
// pair files with the tracks of MusicLib's API; Vibrance reads the tags.
func trackSlot(rel string) (disc, no int, ok bool) {
	disc = 1
	name := rel
	if d, rest, found := strings.Cut(rel, "/"); found {
		n, err := strconv.Atoi(strings.TrimPrefix(d, "Disc "))
		if err != nil || !strings.HasPrefix(d, "Disc ") || strings.Contains(rest, "/") {
			return 0, 0, false
		}
		disc, name = n, rest
	}
	num, _, found := strings.Cut(name, " - ")
	if !found {
		return 0, 0, false
	}
	no, err := strconv.Atoi(num)
	if err != nil {
		return 0, 0, false
	}
	return disc, no, true
}
