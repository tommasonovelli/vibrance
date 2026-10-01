package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A managed field of DESIGN.md §4.3, and the names under which ffprobe may
// report it (lowercase; ffmpeg converts some container names to generic
// ones). A key ffprobe reports that no field claims is listed apart, so a
// name missing here shows up in the report instead of being lost.
type tagField struct {
	Name    string
	Aliases []string
	Want    func(a album, t track) []string // the accepted values
	// Within names the field whose "n/total" value may carry this one (a
	// total), when no key of its own does.
	Within string
}

var tagFields = []tagField{
	{"Title", []string{"title", "tit2", "©nam"}, func(_ album, t track) []string { return []string{t.Title} }, ""},
	{"Artist", []string{"artist", "tpe1", "©art"}, func(a album, t track) []string {
		if t.Artist != nil {
			return []string{*t.Artist}
		}
		return []string{a.ArtistName}
	}, ""},
	{"Album artist", []string{"album_artist", "albumartist", "album artist", "tpe2", "aart"}, func(a album, _ track) []string { return []string{a.ArtistName} }, ""},
	{"Album", []string{"album", "talb", "©alb"}, func(a album, _ track) []string { return []string{a.Title} }, ""},
	{"Track", []string{"track", "tracknumber", "trck", "trkn"}, func(a album, t track) []string {
		n := strconv.Itoa(t.No)
		return []string{n, n + "/" + strconv.Itoa(discTracks(a, t.Disc))}
	}, ""},
	{"Track total", []string{"tracktotal", "totaltracks"}, func(a album, t track) []string { return []string{strconv.Itoa(discTracks(a, t.Disc))} }, "Track"},
	{"Disc", []string{"disc", "discnumber", "tpos", "disk"}, func(a album, t track) []string {
		n := strconv.Itoa(t.Disc)
		return []string{n, n + "/" + strconv.Itoa(discs(a))}
	}, ""},
	{"Disc total", []string{"disctotal", "totaldiscs"}, func(a album, _ track) []string { return []string{strconv.Itoa(discs(a))} }, "Disc"},
	{"Year", []string{"date", "year", "tdrc", "tyer", "©day"}, func(a album, _ track) []string {
		if a.Year == nil {
			return nil
		}
		return []string{strconv.Itoa(*a.Year)}
	}, ""},
	{"Genre", []string{"genre", "tcon", "©gen"}, func(a album, t track) []string {
		if t.Genre != nil {
			return []string{*t.Genre}
		}
		if a.Genre != nil {
			return []string{*a.Genre}
		}
		return nil
	}, ""},
	{"Compilation", []string{"compilation", "tcmp", "cpil"}, func(a album, _ track) []string {
		if a.Compilation {
			return []string{"1"}
		}
		return nil
	}, ""},
}

func discTracks(a album, disc int) int {
	n := 0
	for _, t := range a.Tracks {
		if t.Disc == disc {
			n++
		}
	}
	return n
}

func discs(a album) int {
	n := 0
	for _, t := range a.Tracks {
		n = max(n, t.Disc)
	}
	return n
}

// runTags is H3: it marks each codec album of H2 as a compilation (so that
// every managed field has a value), waits for the render, and tabulates the
// keys ffprobe reports for the second track of each, checked against the
// values of MusicLib's API.
func runTags(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var f fragment
	f.b.WriteString("## H3: the tag keys ffprobe reports\n\n")
	f.para("Each album of H2 is first saved as a compilation (`PUT /api/albums/{id}` with `compilation: true`), so that every field of `DESIGN.md` §4.3 has a value. Then ffprobe reads its track 2 (format and stream tags). A cell is `key = value` for each key that carries the field, with ✓ when the value is the one MusicLib's API reports. ReplayGain is in H9; the cover in H4. `ffprobe` shows format tags without prefix and stream tags with `stream:`.")
	header := []string{"Field"}
	cells := map[string][]string{}
	ok := true
	var dumps []string
	for _, ca := range codecAlbums {
		spec := specByKey(ca.Key)
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return err
		}
		prev, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return err
		}
		u := a.update()
		u.Compilation = true
		if err := c.putAlbum(ctx, a, u); err != nil {
			return err
		}
		p, err := c.waitPublished(ctx, a.ID, prev.Dir.Receipt.BuildID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(p.Album.Tracks, func(t track) bool { return t.Disc == 1 && t.No == 2 })
		if i < 0 {
			return fmt.Errorf("album %s has no track 2", spec.Key)
		}
		t := p.Album.Tracks[i]
		var rel string
		for _, rf := range p.Dir.Receipt.Files {
			if d, n, found := trackSlot(rf.RelativePath); found && isAudio(rf.RelativePath) && d == 1 && n == 2 {
				rel = rf.RelativePath
			}
		}
		pr, run, err := probe(ctx, filepath.Join(libraryRoot, filepath.FromSlash(p.Dir.Rel), filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		tags := map[string]string{}
		for k, v := range pr.Format.Tags {
			tags[k] = v
		}
		st, _ := pr.audio()
		for k, v := range st.Tags {
			tags["stream:"+k] = v
		}
		header = append(header, ca.Codec)
		claimed := map[string]bool{}
		for _, field := range tagFields {
			want := field.Want(p.Album, t)
			var found []string
			fieldOK := false
			for _, k := range sortedKeys(tags) {
				if !slices.Contains(field.Aliases, strings.ToLower(strings.TrimPrefix(k, "stream:"))) {
					continue
				}
				claimed[k] = true
				mark := "✗"
				if slices.Contains(want, tags[k]) {
					mark, fieldOK = "✓", true
				}
				found = append(found, fmt.Sprintf("`%s` = `%s` %s", k, tags[k], mark))
			}
			if !fieldOK && field.Within != "" {
				if k, total, in := withinTotal(field, tags, want); in {
					fieldOK = true
					found = append(found, fmt.Sprintf("in `%s` (`/%s`) ✓", k, total))
				}
			}
			if !fieldOK {
				ok = false
				found = append(found, fmt.Sprintf("(want %q)", want))
			}
			cells[field.Name] = append(cells[field.Name], strings.Join(found, "<br>"))
		}
		var rest, sortKeys []string
		for _, k := range sortedKeys(tags) {
			lk := strings.ToLower(k)
			switch {
			case strings.Contains(lk, "sort"):
				sortKeys = append(sortKeys, k)
			case !claimed[k] && !strings.Contains(lk, "replaygain"):
				rest = append(rest, fmt.Sprintf("`%s` = `%s`", k, tags[k]))
			}
		}
		if len(sortKeys) > 0 {
			ok = false
		}
		cells["Other keys"] = append(cells["Other keys"], strings.Join(rest, "<br>"))
		cells["Sort keys (must be absent)"] = append(cells["Sort keys (must be absent)"], orNone(sortKeys))
		dumps = append(dumps, run.Cmdline+"\n"+string(run.Stdout))
	}
	var rows [][]string
	for _, name := range append(fieldNames(), "Sort keys (must be absent)", "Other keys") {
		rows = append(rows, append([]string{name}, cells[name]...))
	}
	f.table(header, rows)
	f.b.WriteString("<details><summary>The four probes</summary>\n\n")
	for _, d := range dumps {
		cmd, out, _ := strings.Cut(d, "\n")
		f.command(cmd, out)
	}
	f.b.WriteString("</details>\n\n")
	if err := verdict("H3 tag keys", ok,
		"every field of §4.3 is reported by ffprobe for FLAC, MP3, AAC and ALAC with the value MusicLib shows, and no sort tag is present"); err != nil {
		return err
	}
	return f.save("H3")
}

func fieldNames() []string {
	var n []string
	for _, f := range tagFields {
		n = append(n, f.Name)
	}
	return n
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// withinTotal looks for the total of field (Track total, Disc total) inside
// the "n/total" value of the field it names in Within.
func withinTotal(field tagField, tags map[string]string, want []string) (key, total string, found bool) {
	i := slices.IndexFunc(tagFields, func(f tagField) bool { return f.Name == field.Within })
	for _, k := range sortedKeys(tags) {
		if !slices.Contains(tagFields[i].Aliases, strings.ToLower(strings.TrimPrefix(k, "stream:"))) {
			continue
		}
		if _, t, ok := strings.Cut(tags[k], "/"); ok && slices.Contains(want, t) {
			return k, t, true
		}
	}
	return "", "", false
}
