//go:build contract

package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"vibrance/internal/api"
)

// actionTimeout bounds what the host script does for the suite: the
// slowest is a start of MusicLib.
const actionTimeout = 15 * time.Minute

// harness is what the scenarios drive: the two products, MusicLib's library
// read-only, and the host script, which does what only Docker can do.
type harness struct {
	ml      *musiclib
	vb      *vibrance
	library string
	control string
	seq     int
	rng     *rand.Rand
}

// env reads a variable that scripts/contract.sh sets.
func env(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set: the contract suite runs only in scripts/contract.sh", name)
	}
	return v
}

// act asks the host script for an action and waits for its answer. The
// request is a line on the standard output, which the script reads; the
// answer is a file the script writes into the control folder: "ok [text]",
// whose text act returns, or "error text".
func (h *harness) act(t *testing.T, action ...string) string {
	t.Helper()
	h.seq++
	what := strings.Join(action, " ")
	answer := filepath.Join(h.control, strconv.Itoa(h.seq))
	fmt.Printf("@@contract-action %d %s\n", h.seq, what)
	deadline := time.Now().Add(actionTimeout)
	for {
		data, err := os.ReadFile(answer)
		switch {
		case err == nil:
			reply := strings.TrimSpace(string(data))
			if text, ok := strings.CutPrefix(reply, "ok"); ok {
				return strings.TrimSpace(text)
			}
			t.Fatalf("%s: %s", what, reply)
		case !errors.Is(err, fs.ErrNotExist):
			t.Fatalf("reading the answer to %s: %v", what, err)
		case time.Now().After(deadline):
			t.Fatalf("no answer to %s in %s", what, actionTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestContract runs the scenarios of DESIGN.md §12.3 in order, on one
// stack: each starts from the state the ones before left, with the
// playlist and the favorites already on its tracks. The first that fails
// stops the suite.
func TestContract(t *testing.T) {
	seed, err := strconv.ParseUint(env(t, "CONTRACT_SEED"), 10, 64)
	if err != nil {
		t.Fatalf("CONTRACT_SEED: %v", err)
	}
	t.Logf("seed %d (CONTRACT_SEED=%d scripts/contract.sh repeats the random choices)", seed, seed)
	h := &harness{
		ml:      newMusiclib(t, env(t, "MUSICLIB_URL"), env(t, "MUSICLIB_PASSWORD")),
		vb:      newVibrance(t, env(t, "VIBRANCE_URL"), env(t, "VIBRANCE_ADMIN_USERNAME"), env(t, "VIBRANCE_ADMIN_PASSWORD")),
		library: "/musiclib/library",
		control: env(t, "CONTRACT_CONTROL"),
		rng:     rand.New(rand.NewPCG(seed, seed)),
	}
	if err := os.MkdirAll(h.control, 0o700); err != nil {
		t.Fatal(err)
	}
	h.vb.waitReady(t)
	h.vb.login(t)
	h.ml.login(t)

	w := newWorld(h)
	scenarios := []struct {
		name string
		run  func(*testing.T, *world)
	}{
		{"A15 restarts during the first scan", a15},
		{"references on every track", references},
		{"A1 title of a track", a1},
		{"A2 two track numbers swapped", a2},
		{"A3 new cover", a3},
		{"A4 album renamed", a4},
		{"A5 artist renamed", a5},
		{"A6 album moved to another artist", a6},
		{"A7 track deleted", a7},
		{"A8 album to the trash", a8},
		{"A9 album restored", a9},
		{"A10 render of every album", a10},
		{"A11 offline rebuild", a11},
		{"A12 new album", a12},
		{"A13 album written again while playing", a13},
		{"A14 first of two tracks with the same audio deleted", a14},
		{"A16 library unreadable", a16},
		{"A17 track moved to another album", a17},
		{"A18 every track moved", a18},
		{"A19 two tracks swapped between albums", a19},
		{"A20 trash emptied and album imported again", a20},
	}
	for _, s := range scenarios {
		if !t.Run(s.name, func(t *testing.T) { s.run(t, w) }) {
			t.Fatalf("%s failed: the scenarios after it build on it", s.name)
		}
	}
}

// album reads the album of the import folder folder from MusicLib.
func (w *world) album(t *testing.T, folder string) mlAlbum {
	t.Helper()
	return w.h.ml.album(t, w.albums[folder])
}

// key is the MusicLib track tr of album a.
func key(a mlAlbum, tr mlTrack) mlKey { return mlKey{a.ID, tr.ID} }

// setTrack is a change of an album that changes the track id.
func setTrack(id string, change func(*trackUpdate)) func(*albumUpdate) {
	return func(u *albumUpdate) {
		for i := range u.Tracks {
			if u.Tracks[i].ID == id {
				change(&u.Tracks[i])
			}
		}
	}
}

// tracksOf is the Vibrance tracks of the albums of folders, album by album,
// by disc and number.
func (w *world) tracksOf(folders ...string) []string {
	var out []string
	for _, f := range folders {
		at := w.at[w.albums[f]]
		places := make([]place, 0, len(at))
		for p := range at {
			places = append(places, p)
		}
		slices.SortFunc(places, func(a, b place) int {
			if a.disc != b.disc {
				return a.disc - b.disc
			}
			return a.no - b.no
		})
		for _, p := range places {
			out = append(out, at[p])
		}
	}
	return out
}

// addReferences adds the tracks ids at the end of the playlist and to the
// favorites, and records them as they must stay. The items and favorites
// before them must be as they were.
func (w *world) addReferences(t *testing.T, ids []string) {
	t.Helper()
	vb := w.h.vb
	req := api.AddPlaylistItemsRequest{}
	for _, id := range ids {
		req.TrackIds = append(req.TrackIds, uuid.MustParse(id))
	}
	var res api.AddPlaylistItemsResult
	if err := vb.call(t.Context(), http.MethodPost, "/api/v1/playlists/"+w.playlist+"/items", req, http.StatusOK, &res); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := vb.call(t.Context(), http.MethodPut, "/api/v1/me/favorites/tracks/"+id, nil, http.StatusNoContent, nil); err != nil {
			t.Fatal(err)
		}
	}
	var items []wantItem
	for _, it := range vb.items(t, w.playlist) {
		items = append(items, wantItem{id: it.Id, addedAt: it.AddedAt, track: it.Track.Id, position: it.Position})
	}
	if len(items) != len(w.items)+len(ids) || !slices.Equal(items[:len(w.items)], w.items) {
		t.Fatalf("adding %d items changed the items before them: %+v, were %+v", len(ids), items, w.items)
	}
	w.items, w.revision = items, res.Playlist.Revision
	got := make(map[string]string)
	for _, f := range vb.favorites(t) {
		got[f.Track.Id] = f.FavoritedAt
	}
	want := maps.Clone(w.favorites)
	for _, id := range ids {
		if _, ok := want[id]; !ok {
			want[id] = got[id]
		}
	}
	if !maps.Equal(got, want) {
		t.Fatalf("the favorites are %v after adding %v to %v", got, ids, w.favorites)
	}
	w.favorites = got
}
