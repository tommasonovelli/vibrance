package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runImport imports the whole import folder through MusicLib's API (POST
// /api/imports with path ""), waits for the batch and for the render of
// every album, and checks that the albums of albumSpecs, and only they, are
// in the library. It records the run metadata for the report.
func runImport(ctx context.Context) error {
	c, err := login(ctx)
	if err != nil {
		return err
	}
	id, err := newUUID()
	if err != nil {
		return err
	}
	type job struct {
		Kind          string  `json:"kind"`
		State         string  `json:"state"`
		SourceRel     *string `json:"source_rel"`
		ResultAlbumID *string `json:"result_album_id"`
		ErrorCode     *string `json:"error_code"`
		ErrorMessage  *string `json:"error_message"`
		Warnings      []struct {
			Code    string  `json:"code"`
			Message string  `json:"message"`
			Path    *string `json:"path"`
		} `json:"warnings"`
	}
	type report struct {
		State      string `json:"state"`
		Scan       job    `json:"scan"`
		Candidates []job  `json:"candidates"`
	}
	var rep report
	if err := c.call(ctx, http.MethodPost, "/api/imports", nil, map[string]string{"id": id, "path": ""}, &rep); err != nil {
		return err
	}
	deadline := time.Now().Add(waitTimeout)
	for rep.State != "completed" {
		if err := sleep(ctx, deadline, "the import batch"); err != nil {
			return err
		}
		if err := c.call(ctx, http.MethodGet, "/api/imports/"+id, nil, nil, &rep); err != nil {
			return err
		}
	}
	for _, w := range rep.Scan.Warnings {
		fmt.Printf("scan warning: %s %s %s\n", w.Code, deref(w.Path), w.Message)
	}
	if len(rep.Candidates) != len(albumSpecs) {
		return fmt.Errorf("the import found %d albums, want %d: %+v", len(rep.Candidates), len(albumSpecs), rep)
	}
	for _, j := range rep.Candidates {
		if j.State != "done" || j.ResultAlbumID == nil {
			return fmt.Errorf("import of %s: state %s: %s: %s", deref(j.SourceRel), j.State, deref(j.ErrorCode), deref(j.ErrorMessage))
		}
		for _, w := range j.Warnings {
			fmt.Printf("import warning %s: %s %s\n", deref(j.SourceRel), w.Code, w.Message)
		}
	}
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	if len(all) != len(albumSpecs) {
		return fmt.Errorf("MusicLib lists %d albums, want %d", len(all), len(albumSpecs))
	}
	for _, spec := range albumSpecs {
		a, err := c.albumByTitle(ctx, all, spec.Title)
		if err != nil {
			return err
		}
		if a.ArtistName != spec.Artist || len(a.Tracks) != len(spec.Tracks) {
			return fmt.Errorf("album %q: artist %q with %d tracks, want %q with %d", spec.Title, a.ArtistName, len(a.Tracks), spec.Artist, len(spec.Tracks))
		}
		p, err := c.waitPublished(ctx, a.ID, "")
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s (album_id %s, revision %d)\n", spec.Key, p.Dir.Rel, a.ID, p.Dir.Receipt.AlbumRevision)
	}
	return writeMeta(ctx, c)
}

// writeMeta records what the run used, for the report and FIXTURE.md:
// MusicLib's version and render_version, the ffmpeg version, the time.
func writeMeta(ctx context.Context, c *client) error {
	all, err := c.albums(ctx)
	if err != nil {
		return err
	}
	var st albumStatus
	if err := c.call(ctx, http.MethodGet, "/api/albums/"+all[0].ID+"/status", nil, nil, &st); err != nil {
		return err
	}
	ff, err := toolVersion(ctx)
	if err != nil {
		return err
	}
	image := os.Getenv("MUSICLIB_IMAGE")
	m := map[string]string{
		"musiclib_image":  image,
		"render_version":  st.Renderer,
		"ffmpeg_version":  ff,
		"generated_at":    stamp(),
		"debian_snapshot": debianSnapshot,
		"lame":            lamePackage,
		"atomicparsley":   parsleyPackage,
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeWorkFile("meta.json", raw); err != nil {
		return err
	}
	fixture, err := fixtureRenderVersion(filepath.Join(srcRoot, fixtureDir), st.Renderer)
	if err != nil {
		return err
	}
	md := fmt.Sprintf("- Run: %s, MusicLib `%s`.\n- `render_version`: `%s`.\n- Tools: `%s` (the Vibrance toolchain image, copied from MusicLib's image).\n- Inputs: the pinned ffmpeg; MP3 by `%s`, M4A ReplayGain atoms by `%s`, from the Debian snapshot `%s`.\n- Fixture: %s\n",
		m["generated_at"], image, st.Renderer, ff, lamePackage, parsleyPackage, debianSnapshot, fixture)
	return writeWorkFile("meta.md", []byte(md))
}

// toolVersion is the first line of `ffmpeg -version`.
func toolVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	out, err := execOutput(ctx, ffmpegPath, "-hide_banner", "-version")
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(out, "\n")
	return first, nil
}

// fixtureRenderVersion compares the render_version of every receipt of the
// committed fixture library at dir with current, the one MusicLib writes in
// this run, and says so in one sentence for the report. While they are
// equal the fixture is what this MusicLib would write, and need not be
// regenerated (DESIGN.md §12.2).
func fixtureRenderVersion(dir, current string) (string, error) {
	dirs, err := scanLibrary(dir)
	if err != nil {
		return "", fmt.Errorf("reading the fixture library: %w", err)
	}
	var other []string
	for _, d := range dirs {
		if v := d.Receipt.RenderVersion; v != current && !slices.Contains(other, v) {
			other = append(other, v)
		}
	}
	if len(dirs) > 0 && len(other) == 0 {
		return fmt.Sprintf("the %d receipts of `%s/` carry this same `render_version`.", len(dirs), fixtureDir), nil
	}
	return fmt.Sprintf("the %d receipts of `%s/` carry another `render_version` (`%s`): regenerate it with `scripts/make-fixture-library.sh`.",
		len(dirs), fixtureDir, strings.Join(other, "`, `")), nil
}
