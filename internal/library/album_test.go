package library

import (
	"math/rand/v2"
	"testing"
)

// tags is a track at a place, with the album tags MusicLib writes.
func tags(disc, no int, album, albumArtist string) TrackTags {
	return TrackTags{Disc: disc, No: no, Album: album, AlbumArtist: albumArtist, Artist: albumArtist}
}

func TestDeriveAlbum(t *testing.T) {
	tests := []struct {
		name   string
		tracks []TrackTags
		want   AlbumMeta
	}{
		{
			name: "an album as MusicLib writes it: every track says the same",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "Kind of Blue", AlbumArtist: "Miles Davis", Artist: "Miles Davis", Genre: "Jazz", Year: 1959},
				{Disc: 1, No: 2, Album: "Kind of Blue", AlbumArtist: "Miles Davis", Artist: "Miles Davis feat. Bill Evans", Genre: "Jazz", Year: 1959},
				{Disc: 1, No: 3, Album: "Kind of Blue", AlbumArtist: "Miles Davis", Artist: "Miles Davis", Genre: "Jazz", Year: 1959},
			},
			want: AlbumMeta{Title: "Kind of Blue", Artist: "Miles Davis", Year: 1959, Genre: "Jazz"},
		},
		{
			name:   "no track",
			tracks: nil,
			want:   AlbumMeta{Title: "Unknown Album", Artist: "Unknown Artist"},
		},
		{
			name:   "tracks without any tag",
			tracks: []TrackTags{{Disc: 1, No: 1}, {Disc: 1, No: 2}},
			want:   AlbumMeta{Title: "Unknown Album", Artist: "Unknown Artist"},
		},

		// Title.
		{
			name:   "title: the one most tracks have",
			tracks: []TrackTags{tags(1, 1, "B", "X"), tags(1, 2, "A", "X"), tags(1, 3, "A", "X")},
			want:   AlbumMeta{Title: "A", Artist: "X"},
		},
		{
			name:   "title: of two that as many tracks have, the one of the first track",
			tracks: []TrackTags{tags(1, 2, "A", "X"), tags(1, 1, "B", "X"), tags(1, 3, "A", "X"), tags(1, 4, "B", "X")},
			want:   AlbumMeta{Title: "B", Artist: "X"},
		},
		{
			name:   "title: the first track is by disc, then by number",
			tracks: []TrackTags{tags(2, 1, "A", "X"), tags(1, 9, "B", "X")},
			want:   AlbumMeta{Title: "B", Artist: "X"},
		},
		{
			name:   "title: two tracks in one place are decided by the bytes",
			tracks: []TrackTags{tags(1, 1, "b", "X"), tags(1, 1, "a", "X")},
			want:   AlbumMeta{Title: "a", Artist: "X"},
		},
		{
			name:   "title: a track without the tag does not count",
			tracks: []TrackTags{tags(1, 1, "", "X"), tags(1, 2, " \t", "X"), tags(1, 3, "A", "X")},
			want:   AlbumMeta{Title: "A", Artist: "X"},
		},
		{
			name: "title: compared in NFC and without the spaces around it",
			tracks: []TrackTags{
				tags(1, 1, "Other", "X"), tags(1, 2, "Caf\u00e9", "X"), tags(1, 3, " Cafe\u0301\n", "X"), tags(1, 4, "Other", "X"),
				tags(1, 5, "Caf\u00e9 ", "X"),
			},
			want: AlbumMeta{Title: "Caf\u00e9", Artist: "X"},
		},
		{
			name:   "title: the case tells two titles apart",
			tracks: []TrackTags{tags(1, 1, "abc", "X"), tags(1, 2, "ABC", "X"), tags(1, 3, "ABC", "X")},
			want:   AlbumMeta{Title: "ABC", Artist: "X"},
		},

		// Artist.
		{
			name: "artist: the album artist most tracks have",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "B", Artist: "Z"},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "A", Artist: "Z"},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "A", Artist: "Z"},
			},
			want: AlbumMeta{Title: "T", Artist: "A"},
		},
		{
			name: "artist: one album artist is worth more than every track artist",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", Artist: "Z"},
				{Disc: 1, No: 2, Album: "T", Artist: "Z"},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "A", Artist: "Z"},
			},
			want: AlbumMeta{Title: "T", Artist: "A"},
		},
		{
			name: "artist: without any album artist, the track artist most tracks have",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", Artist: "Y"},
				{Disc: 1, No: 2, Album: "T", Artist: "Z", AlbumArtist: "  "},
				{Disc: 1, No: 3, Album: "T", Artist: "Z"},
			},
			want: AlbumMeta{Title: "T", Artist: "Z"},
		},
		{
			name: "artist: of two that as many tracks have, the one of the first track",
			tracks: []TrackTags{
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "A"},
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "B"},
			},
			want: AlbumMeta{Title: "T", Artist: "B"},
		},
		{
			name: "artist: the name as it is, in NFC and without the spaces around it",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "  Bjo\u0308rk  "},
			},
			want: AlbumMeta{Title: "T", Artist: "Bj\u00f6rk"},
		},
		{
			name:   "artist: Various Artists is an artist like any other",
			tracks: []TrackTags{{Disc: 1, No: 1, Album: "T", AlbumArtist: "Various Artists", Artist: "Someone", Compilation: true}},
			want:   AlbumMeta{Title: "T", Artist: "Various Artists", Compilation: true},
		},

		// Year.
		{
			name: "year: the one most tracks have",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Year: 1958},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Year: 1959},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "X", Year: 1959},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Year: 1959},
		},
		{
			name: "year: of two that as many tracks have, the lower",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Year: 1960},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Year: 1959},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Year: 1959},
		},
		{
			name: "year: a value that is not a year does not count",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Year: 0},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Year: 0},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "X", Year: 10000},
				{Disc: 1, No: 4, Album: "T", AlbumArtist: "X", Year: 10000},
				{Disc: 1, No: 5, Album: "T", AlbumArtist: "X", Year: -1959},
				{Disc: 1, No: 6, Album: "T", AlbumArtist: "X", Year: -1959},
				{Disc: 1, No: 7, Album: "T", AlbumArtist: "X", Year: 9999},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Year: 9999},
		},
		{
			name: "year: the first year is a year",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Year: 1},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Year: 1},
		},

		// Genre.
		{
			name: "genre: the one most tracks have",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Genre: "Blues"},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Genre: "Jazz"},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "X", Genre: " Jazz "},
				{Disc: 1, No: 4, Album: "T", AlbumArtist: "X"},
				{Disc: 1, No: 5, Album: "T", AlbumArtist: "X"},
				{Disc: 1, No: 6, Album: "T", AlbumArtist: "X"},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Genre: "Jazz"},
		},
		{
			name: "genre: of two that as many tracks have, the first by the bytes, whatever the place",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Genre: "Jazz"},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Genre: "Blues"},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Genre: "Blues"},
		},
		{
			name: "genre: several values are one text",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X", Genre: "Jazz; Blues"},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Genre: "Jazz; Blues"},
		},

		// Compilation.
		{
			name: "compilation: one track is enough",
			tracks: []TrackTags{
				{Disc: 1, No: 1, Album: "T", AlbumArtist: "X"},
				{Disc: 1, No: 2, Album: "T", AlbumArtist: "X", Compilation: true},
				{Disc: 1, No: 3, Album: "T", AlbumArtist: "X"},
			},
			want: AlbumMeta{Title: "T", Artist: "X", Compilation: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveAlbum(tc.tracks); got != tc.want {
				t.Errorf("DeriveAlbum = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// DeriveAlbum does not depend on the order of the tracks, and it does not
// change them. The tracks take their values from a few, so that two values
// with as many tracks, and two tracks in one place, are the usual case.
func TestDeriveAlbumWhateverTheOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	texts := []string{"", "", " ", "A", "B", "b", " A", "Caf\u00e9", "Cafe\u0301", "Zed"}
	years := []int{0, 0, -1, 1958, 1959, 1960, 10000}
	text := func() string { return texts[rng.IntN(len(texts))] }
	differing := map[AlbumMeta]bool{}
	for range 5000 {
		tracks := make([]TrackTags, rng.IntN(7))
		for i := range tracks {
			tracks[i] = TrackTags{
				Disc: 1 + rng.IntN(2), No: 1 + rng.IntN(3),
				Album: text(), AlbumArtist: text(), Artist: text(), Genre: text(),
				Year: years[rng.IntN(len(years))], Compilation: rng.IntN(6) == 0,
			}
		}
		given := append([]TrackTags(nil), tracks...)
		want := DeriveAlbum(tracks)
		differing[want] = true
		if want.Title == "" || want.Artist == "" {
			t.Fatalf("DeriveAlbum(%+v) = %+v: an album without a title or an artist", tracks, want)
		}
		for range 4 {
			rng.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
			shuffled := append([]TrackTags(nil), tracks...)
			if got := DeriveAlbum(tracks); got != want {
				t.Fatalf("DeriveAlbum(%+v) = %+v, and with the tracks in the order %+v it is %+v", given, want, shuffled, got)
			}
			for i := range tracks {
				if tracks[i] != shuffled[i] {
					t.Fatalf("DeriveAlbum changed its argument: %+v, was %+v", tracks, shuffled)
				}
			}
		}
	}
	if len(differing) < 100 {
		t.Fatalf("only %d different albums were derived: the generator is too poor", len(differing))
	}
}
