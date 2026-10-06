//go:build perf

package perfgen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/auth"
	"vibrance/internal/library"
	"vibrance/internal/media"
	"vibrance/internal/names"
	"vibrance/internal/search"
	"vibrance/internal/store"
)

// Size is how much a dataset holds.
type Size struct {
	Artists, Albums, Tracks int
	Users                   int
	// Playlists is how many playlists there are in all, each of
	// PlaylistItems items.
	Playlists, PlaylistItems int
	// Favorites is how many favorites there are in all, shared equally
	// among the users.
	Favorites int
}

// Full is the dataset the budgets of the step S24 are stated for.
var Full = Size{Artists: 2000, Albums: 20000, Tracks: 200000, Users: 20, Playlists: 500, PlaylistItems: 200, Favorites: 50000}

// The accounts of a dataset. Every account has the password given to
// Generate. Admin owns the playlists: 500 is the most a user can have
// (DESIGN.md §5.2), and the list of the playlists of a user is measured at
// that limit. User is an account without playlists.
const (
	Admin = "perf-admin"
	User  = "perf-user-01"
)

// playlistsPerUser is catalog.MaxPlaylists: a dataset with more playlists
// gives the ones beyond it to the next users.
const playlistsPerUser = 500

// The names in the folder of MusicLib (DESIGN.md §4.1, §4.5).
const (
	libraryFolder = "library"
	storeMarker   = ".musiclib-store"
	renderVersion = "musiclib-render/3 names/1 go1.25.14 ffmpeg/8.1.3-musiclib1 musiclib-tags/4 taglib/2.3.2-musiclib1"
)

// epoch is when the first album of a dataset was seen: a fixed moment, so
// that the same size always gives the same rows.
const epoch = int64(1_600_000_000_000)

// albumsPerTx is how many albums one write transaction of the generator
// holds. The indexer commits one album at a time (DESIGN.md §6.3); the
// generator writes the same rows with the same queries, in larger
// transactions only because nothing reads the database meanwhile.
const albumsPerTx = 250

// Generate fills st, a new database, with a synthetic index of that size
// and the data of its users: what the scanner would have written for a
// library of that many albums, through the queries the scanner uses, and
// the accounts, the playlists and the favorites through those of the API.
// The rows are the same at every call with the same size. The caller then
// lets SQLite analyze them (Store.Optimize), as the scanner does at the end
// of a cycle that wrote many albums (DESIGN.md §6.1, P6).
//
// With a musiclibDir that is not "", it also writes there what MusicLib
// would have: the marker of its volume and, for every album, its folder in
// library/ with a real receipt, the one the index says it was indexed from.
// The track files are not written: a scan that finds the receipts unchanged
// never opens them.
func Generate(ctx context.Context, st *store.Store, size Size, password, musiclibDir string) error {
	g := &generator{size: size, rng: rand.New(rand.NewPCG(24, uint64(size.Albums))), musiclibDir: musiclibDir,
		artistOf: map[string]bool{}, folders: map[string]bool{}}
	if err := g.checkSize(); err != nil {
		return err
	}
	if musiclibDir != "" {
		marker := "store_id=" + g.uuid7(epoch) + "\n"
		if err := os.WriteFile(filepath.Join(musiclibDir, storeMarker), []byte(marker), 0o644); err != nil {
			return fmt.Errorf("perfgen: %w", err)
		}
		if err := os.Mkdir(filepath.Join(musiclibDir, libraryFolder), 0o755); err != nil {
			return fmt.Errorf("perfgen: %w", err)
		}
	}
	g.makeArtists()
	for first := 0; first < size.Albums; first += albumsPerTx {
		albums := make([]album, 0, albumsPerTx)
		for i := first; i < min(first+albumsPerTx, size.Albums); i++ {
			a, err := g.makeAlbum(i)
			if err != nil {
				return err
			}
			albums = append(albums, a)
		}
		if err := st.WithWriteTx(ctx, func(q *store.Queries) error { return writeAlbums(ctx, q, albums) }); err != nil {
			return fmt.Errorf("perfgen: writing the albums from %d: %w", first, err)
		}
	}
	users, err := makeUsers(ctx, st, size.Users, password)
	if err != nil {
		return err
	}
	if err := g.writeFavorites(ctx, st, users); err != nil {
		return err
	}
	return g.writePlaylists(ctx, st, users)
}

type generator struct {
	size        Size
	rng         *rand.Rand
	musiclibDir string
	artists     []artist
	// artistOf has the identity keys of the artists, and folders the album
	// folders: neither is given twice.
	artistOf map[string]bool
	folders  map[string]bool
	// trackIDs are the ids of every track written, in order.
	trackIDs []string
}

type artist struct {
	row      store.UpsertArtistParams
	language *language
}

// album is one album as the index has it.
type album struct {
	artist store.UpsertArtistParams
	row    store.UpsertAlbumParams
	tracks []store.UpsertTrackParams
}

func (g *generator) checkSize() error {
	s := g.size
	switch {
	case s.Artists < 1 || s.Albums < s.Artists || s.Tracks < s.Albums:
		return errors.New("perfgen: a dataset has at least one artist, an album for each artist and a track for each album")
	case s.Users < 2:
		return errors.New("perfgen: a dataset has at least two accounts")
	case s.Playlists > playlistsPerUser*s.Users:
		return errors.New("perfgen: a user has at most 500 playlists")
	case s.Favorites/s.Users > s.Tracks:
		return errors.New("perfgen: more favorites for a user than tracks")
	}
	return nil
}

// uuid7 is an id with the shape of a UUIDv7 of that moment, as the ids of
// MusicLib's albums and of Vibrance's tracks are, from the generator of
// the dataset and not from the clock.
func (g *generator) uuid7(ms int64) string {
	var u uuid.UUID
	binary.BigEndian.PutUint64(u[:8], uint64(ms)<<16|g.rng.Uint64()&0x0fff)
	binary.BigEndian.PutUint64(u[8:], g.rng.Uint64())
	u[6] = u[6]&0x0f | 0x70
	u[8] = u[8]&0x3f | 0x80
	return u.String()
}

// sum is a SHA-256 in hex that stands for the content of the nth thing of
// a kind: a file, the audio of a track.
func sum(kind string, n int) string {
	h := sha256.Sum256([]byte(kind + ":" + strconv.Itoa(n)))
	return hex.EncodeToString(h[:])
}

func (g *generator) makeArtists() {
	for len(g.artists) < g.size.Artists {
		l := speak(g.rng)
		name := artistName(g.rng, l)
		for try := 2; g.artistOf[names.IdentityKey(name)]; try++ {
			// The pools are small: the later artists of a language need
			// one word more to have a name of their own.
			name = artistName(g.rng, l)
			if try > 4 {
				name += " " + pick(g.rng, l.family)
			}
		}
		g.artistOf[names.IdentityKey(name)] = true
		g.artists = append(g.artists, artist{language: l,
			row: store.UpsertArtistParams{ID: names.ArtistID(name), Name: name, SortKey: names.SortKey(name)}})
	}
}

// artistOfAlbum gives every artist one album, and the albums left to few
// artists more than to the others, as in a real collection: the first
// artists have hundreds.
func (g *generator) artistOfAlbum(i int) artist {
	if i < len(g.artists) {
		return g.artists[i]
	}
	u := g.rng.Float64()
	return g.artists[int(u*u*float64(len(g.artists)))]
}

// tracksOfAlbum shares the tracks among the albums: from 4 less to 4 more
// than the average, and exactly size.Tracks in all.
func (g *generator) tracksOfAlbum(i int) int {
	n := g.size.Tracks / g.size.Albums
	if i < g.size.Tracks%g.size.Albums {
		n++
	}
	if n > 4 && i < g.size.Albums/5*5 {
		n += []int{-4, -2, 0, 2, 4}[i%5]
	}
	return n
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }

// The audio formats of the tracks, flac the most.
var formats = []struct {
	codec, ext        string
	bitDepth, bitrate int64 // 0: none
}{
	{"flac", ".flac", 16, 0}, {"flac", ".flac", 16, 0}, {"flac", ".flac", 16, 0}, {"flac", ".flac", 24, 0},
	{"flac", ".flac", 16, 0}, {"flac", ".flac", 16, 0}, {"flac", ".flac", 24, 0},
	{"mp3", ".mp3", 0, 320000}, {"aac", ".m4a", 0, 256000}, {"alac", ".m4a", 16, 0},
}

// makeAlbum makes the ith album with its tracks and, when the dataset has a
// folder of MusicLib, writes its folder and its receipt.
func (g *generator) makeAlbum(i int) (album, error) {
	rng := g.rng
	ar := g.artistOfAlbum(i)
	l := ar.language
	// A few albums come in at the same moment, a batch of imports.
	seen := epoch + int64(i/4)*60_000
	a := album{artist: ar.row}
	albumTitle := title(rng, l)
	rel := folder(ar.row.Name) + "/" + folder(albumTitle)
	for n := 2; g.folders[rel]; n++ {
		rel = folder(ar.row.Name) + "/" + folder(albumTitle) + " (" + strconv.Itoa(n) + ")"
	}
	g.folders[rel] = true
	a.row = store.UpsertAlbumParams{
		ID: g.uuid7(seen), ArtistID: ar.row.ID, ArtistKey: ar.row.SortKey, Title: albumTitle, TitleKey: names.SortKey(albumTitle),
		YearKey: 10000, RelPath: rel, AlbumRevision: int64(1 + rng.IntN(9)), RenderVersion: renderVersion,
		FirstSeenAt: seen, UpdatedAt: seen,
	}
	if rng.IntN(20) != 0 {
		year := int64(1950 + rng.IntN(75))
		a.row.Year, a.row.YearKey = nullInt(year), year
	}
	albumGenre := ""
	if rng.IntN(10) != 0 {
		albumGenre = pick(rng, genres)
	}
	a.row.Genre = nullString(albumGenre)
	compilation := rng.IntN(33) == 0
	if compilation {
		a.row.Compilation = 1
	}
	classical := albumGenre == "Classical"
	format := formats[rng.IntN(len(formats))]

	var files []library.ReceiptFile
	if rng.IntN(5) != 0 {
		cover := library.ReceiptFile{Path: "cover.jpg", Size: int64(40_000 + rng.IntN(900_000)), SHA256: sum("cover", i)}
		files = append(files, cover)
		a.row.CoverRel, a.row.CoverSha256, a.row.CoverMime = nullString(cover.Path), nullString(cover.SHA256), nullString("image/jpeg")
		a.row.CoverSize, a.row.CoverMtimeNs = nullInt(cover.Size), nullInt(seen*1_000_000)
	}
	n := g.tracksOfAlbum(i)
	firstOfSecondDisc := n + 1
	if n >= 14 {
		firstOfSecondDisc = n/2 + 1
	}
	for no := 1; no <= n; no++ {
		t := store.UpsertTrackParams{
			ID: g.uuid7(seen), AlbumID: a.row.ID, Fingerprint: sum("audio", len(g.trackIDs)), FpVersion: media.PinnedVersion,
			Occurrence: 1, Disc: 1, No: int64(no), Title: title(rng, l), Artist: ar.row.Name, Genre: a.row.Genre,
			FileMtimeNs: seen * 1_000_000, FileSha256: sum("file", len(g.trackIDs)),
			Codec: format.codec, SampleRate: 44100, Channels: 2, UpdatedAt: seen,
		}
		dir := ""
		if firstOfSecondDisc <= n {
			dir = "Disc 1/"
			if no >= firstOfSecondDisc {
				t.Disc, t.No, dir = 2, int64(no-firstOfSecondDisc+1), "Disc 2/"
			}
		}
		switch {
		case classical:
			t.Title = movement(rng, 1+i%9, no)
		case compilation:
			t.Artist = g.artists[rng.IntN(len(g.artists))].row.Name
		case rng.IntN(20) == 0:
			t.Artist += " feat. " + g.artists[rng.IntN(len(g.artists))].row.Name
		}
		t.TitleKey, t.ArtistKey, t.FirstSeenAt = names.SortKey(t.Title), names.SortKey(t.Artist), seen
		duration := int64(90_000 + rng.IntN(390_000))
		bitrate := int64(900_000)
		if format.bitDepth != 0 {
			t.BitDepth = nullInt(format.bitDepth)
			if format.bitDepth == 24 {
				t.SampleRate, bitrate = 96000, 2_800_000
			}
		}
		if format.bitrate != 0 {
			bitrate = format.bitrate
			t.Bitrate = nullInt(bitrate)
		}
		t.FileSize = duration * bitrate / 8000
		// A container that declares no duration is rare (DESIGN.md §4.6).
		if rng.IntN(200) != 0 {
			t.DurationMs = nullInt(duration)
		}
		base := fmt.Sprintf("%02d - %s", t.No, folder(t.Title))
		t.RelPath = dir + base + format.ext
		files = append(files, library.ReceiptFile{Path: t.RelPath, Size: t.FileSize, SHA256: t.FileSha256})
		if rng.IntN(10) == 0 {
			lyrics := library.ReceiptFile{Path: dir + base + ".lrc", Size: int64(200 + rng.IntN(3000)), SHA256: sum("lyrics", len(g.trackIDs))}
			files = append(files, lyrics)
			t.LyricsRel, t.LyricsSha256 = nullString(lyrics.Path), nullString(lyrics.SHA256)
		}
		if rng.IntN(3) == 0 {
			t.RgTrackGain = sql.NullFloat64{Float64: -12 + rng.Float64()*12, Valid: true}
			t.RgTrackPeak = sql.NullFloat64{Float64: 0.5 + rng.Float64()/2, Valid: true}
		}
		g.trackIDs = append(g.trackIDs, t.ID)
		a.tracks = append(a.tracks, t)
	}

	slices.SortFunc(files, func(x, y library.ReceiptFile) int { return strings.Compare(x.Path, y.Path) })
	receipt, err := encodeReceipt(library.Receipt{AlbumID: a.row.ID, BuildID: g.uuid7(seen), AlbumRevision: a.row.AlbumRevision,
		RenderVersion: renderVersion, Files: files})
	if err != nil {
		return album{}, err
	}
	a.row.ReceiptHash = library.ReceiptHash(receipt)
	if g.musiclibDir != "" {
		dir := filepath.Join(g.musiclibDir, libraryFolder, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return album{}, fmt.Errorf("perfgen: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, library.ReceiptName), receipt, 0o644); err != nil {
			return album{}, fmt.Errorf("perfgen: %w", err)
		}
	}
	return a, nil
}

// encodeReceipt writes a receipt as MusicLib does (DESIGN.md §4.2): one
// line of compact JSON, the fields in this order.
func encodeReceipt(r library.Receipt) ([]byte, error) {
	type file struct {
		Path   string `json:"relative_path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	doc := struct {
		SchemaVersion int    `json:"schema_version"`
		AlbumID       string `json:"album_id"`
		BuildID       string `json:"build_id"`
		AlbumRevision int64  `json:"album_revision"`
		RenderVersion string `json:"render_version"`
		Files         []file `json:"files"`
	}{SchemaVersion: 1, AlbumID: r.AlbumID, BuildID: r.BuildID, AlbumRevision: r.AlbumRevision, RenderVersion: r.RenderVersion}
	for _, f := range r.Files {
		doc.Files = append(doc.Files, file(f))
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("perfgen: encoding a receipt: %w", err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// writeAlbums is what the indexer commits for each album (DESIGN.md §6.3
// step 8): its artist, the album, its tracks, its counters and the
// full-text rows of all of them.
func writeAlbums(ctx context.Context, q *store.Queries, albums []album) error {
	for _, a := range albums {
		if err := q.UpsertArtist(ctx, a.artist); err != nil {
			return err
		}
		if err := q.UpsertAlbum(ctx, a.row); err != nil {
			return err
		}
		for _, t := range a.tracks {
			if err := q.UpsertTrack(ctx, t); err != nil {
				return err
			}
		}
		if err := q.SetAlbumKeyOfTracks(ctx, store.SetAlbumKeyOfTracksParams{AlbumKey: a.row.TitleKey, AlbumID: a.row.ID}); err != nil {
			return err
		}
		if err := q.UpdateAlbumCounters(ctx, a.row.ID); err != nil {
			return err
		}
		if err := search.SyncAlbum(ctx, q.Conn(), a.row.ID); err != nil {
			return err
		}
		if err := search.SyncArtist(ctx, q.Conn(), a.artist.ID); err != nil {
			return err
		}
	}
	return nil
}

// makeUsers creates the accounts, as the API does, and returns their ids:
// Admin, then User, then the others.
func makeUsers(ctx context.Context, st *store.Store, n int, password string) ([]string, error) {
	accounts, err := auth.NewService(st, auth.ProductionCost(), time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return nil, fmt.Errorf("perfgen: %w", err)
	}
	ids := make([]string, 0, n)
	for i := range n {
		name, role := fmt.Sprintf("perf-user-%02d", i), auth.RoleUser
		if i == 0 {
			name, role = Admin, auth.RoleAdmin
		}
		u, err := accounts.CreateUser(ctx, name, password, role)
		if err != nil {
			return nil, fmt.Errorf("perfgen: creating the account %s: %w", name, err)
		}
		ids = append(ids, u.ID)
	}
	return ids, nil
}

// writeFavorites gives every user its share of the favorites, on tracks
// that are all different. One favorite in ten has the moment of the one
// before it: the order of the list then falls back on the id.
func (g *generator) writeFavorites(ctx context.Context, st *store.Store, users []string) error {
	for _, user := range users {
		taken := map[int]bool{}
		at := epoch
		err := st.WithWriteTx(ctx, func(q *store.Queries) error {
			for len(taken) < g.size.Favorites/len(users) {
				track := g.rng.IntN(len(g.trackIDs))
				if taken[track] {
					continue
				}
				taken[track] = true
				if g.rng.IntN(10) != 0 {
					at += int64(1 + g.rng.IntN(3_600_000))
				}
				if err := q.AddFavorite(ctx, store.AddFavoriteParams{TrackID: g.trackIDs[track], CreatedAt: at, UserID: user}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("perfgen: writing the favorites: %w", err)
		}
	}
	return nil
}

// writePlaylists writes the playlists, 500 for each user from the first,
// with their items: any track, also twice.
func (g *generator) writePlaylists(ctx context.Context, st *store.Store, users []string) error {
	for p := range g.size.Playlists {
		at := epoch + int64(p)*86_400_000
		id := g.uuid7(at)
		name := title(g.rng, speak(g.rng)) + " " + strconv.Itoa(p+1)
		err := st.WithWriteTx(ctx, func(q *store.Queries) error {
			created, err := q.CreatePlaylist(ctx, store.CreatePlaylistParams{ID: id, Name: name, Description: title(g.rng, english),
				CreatedAt: at, UserID: users[p/playlistsPerUser]})
			if err != nil || created != 1 {
				return fmt.Errorf("creating the playlist %d: %d rows, %w", p, created, err)
			}
			for position := range g.size.PlaylistItems {
				err := q.InsertPlaylistItem(ctx, store.InsertPlaylistItemParams{ID: g.uuid7(at), PlaylistID: id,
					TrackID: g.trackIDs[g.rng.IntN(len(g.trackIDs))], Position: int64(position), AddedAt: at})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("perfgen: writing the playlists: %w", err)
		}
	}
	return nil
}
