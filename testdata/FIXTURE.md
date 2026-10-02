# The fixture library

`testdata/library-v1/` is a real `library/` folder written by MusicLib (`DESIGN.md` §12.2): tiny synthetic albums (a 2-second sine per track), imported and rendered by the published MusicLib, then copied byte for byte. Tests read it; tests that change albums work on a temporary copy.

- MusicLib: `ghcr.io/tommasonovelli/musiclib:1.1.0@sha256:14f63bcde28f4c586a775656f6b3e77e3f0129c2e1339f7e957dca04351f9cb1`, `render_version` `musiclib-render/3 names/1 go1.25.14 ffmpeg/8.1.3-musiclib1 musiclib-tags/4 taglib/2.3.2-musiclib1`.
- Still what MusicLib writes: the pin of the `Dockerfile` is now `ghcr.io/tommasonovelli/musiclib:1.2.0@sha256:52204bdf0ca23eeae71453f2ae8a1ee0a8d9b52ef4ce58df42ace369ab8012dd` (step S6m), and MusicLib 1.2.0 writes the same `render_version`, with the same `ffmpeg` and `ffprobe` (the two binaries have the same SHA-256 in both images), so the fixture was not regenerated. The proof is the line `Fixture` at the head of `docs/spike-report.md`: `scripts/spike.sh` compares the `render_version` that the real MusicLib 1.2.0 reports with the one in every receipt of this folder (NOTES.md N-052).
- Generated: 2026-09-30T22:05:29Z, by `scripts/make-fixture-library.sh`.
- Input audio: `ffmpeg version 8.1.3-musiclib1 Copyright (c) 2000-2026 the FFmpeg developers` (the pinned ffmpeg); MP3 by `lame=3.100-6+b3` and the M4A ReplayGain atoms by `atomicparsley=20240608.083822.1ed9031-1`, from the Debian snapshot `http://snapshot.debian.org/archive/debian/20260926T000000Z`.

## Albums

| | Folder | album_id | Tracks | What it covers |
|---|---|---|---|---|
| A | `Aurora Sines/Alpha_ Light_` | `01a0f459-ebe7-73fe-9598-e85475ef5cca` | 3 | FLAC, three tracks, cover.jpg, lyrics (.lrc) on track 1, ReplayGain, a featuring artist on track 2, characters that MusicLib replaces in names (`:` and `?`) |
| B | `Bravo Tones/Beta MP3` | `01a0f459-ebc8-7081-991c-2b1c9331e156` | 2 | MP3 (LAME), two tracks, ReplayGain |
| C | `Charlie Waves/Gamma AAC` | `01a0f459-ebc2-7821-a442-bd56dc181ce3` | 2 | M4A with AAC, two tracks, ReplayGain |
| D | `Delta Pulse/Delta ALAC` | `01a0f459-ebc1-7b68-9fa3-85c82e63e1b1` | 2 | M4A with ALAC, two tracks, ReplayGain |
| E | `Écho Café/Epsilon Discs` | `01a0f459-eca5-7127-a52f-ab0f69357431` | 3 | FLAC on two discs (`Disc 1/`, `Disc 2/`), a non-ASCII artist name |
| F | `Foxtrot Twins/Phi Same Audio` | `01a0f459-ec9d-7dc9-825a-074f90185adc` | 2 | FLAC, two tracks with the same audio (same fingerprint, different tags) |

Every track also carries the managed tags MusicLib writes (title, artist, album artist, album, track and disc numbers with totals, year, genre); an album with a cover has it embedded in every track as well.

## Files

23 files, 437139 bytes in total (the limit is 2 MiB). `.gitattributes` marks the folder `-text`, so Git never changes a byte: the SHA-256 in each `.musiclib.json` must keep matching.

```text
    1022  Aurora Sines/Alpha_ Light_/.musiclib.json
   29765  Aurora Sines/Alpha_ Light_/01 - First Light.flac
      86  Aurora Sines/Alpha_ Light_/01 - First Light.lrc
   29874  Aurora Sines/Alpha_ Light_/02 - Second Wave.flac
   29973  Aurora Sines/Alpha_ Light_/03 - Third_.flac
     660  Aurora Sines/Alpha_ Light_/Extras/cover.jpg
     660  Aurora Sines/Alpha_ Light_/cover.jpg
     510  Bravo Tones/Beta MP3/.musiclib.json
   33591  Bravo Tones/Beta MP3/01 - One.mp3
   33591  Bravo Tones/Beta MP3/02 - Two.mp3
     510  Charlie Waves/Gamma AAC/.musiclib.json
   27148  Charlie Waves/Gamma AAC/01 - One.m4a
   27031  Charlie Waves/Gamma AAC/02 - Two.m4a
     510  Delta Pulse/Delta ALAC/.musiclib.json
   36488  Delta Pulse/Delta ALAC/01 - One.m4a
   37023  Delta Pulse/Delta ALAC/02 - Two.m4a
     532  Foxtrot Twins/Phi Same Audio/.musiclib.json
   29531  Foxtrot Twins/Phi Same Audio/01 - Same Audio.flac
   29537  Foxtrot Twins/Phi Same Audio/02 - Same Audio Again.flac
     701  Écho Café/Epsilon Discs/.musiclib.json
   29325  Écho Café/Epsilon Discs/Disc 1/01 - Disc One Track One.flac
   29578  Écho Café/Epsilon Discs/Disc 1/02 - Disc One Track Two.flac
   29493  Écho Café/Epsilon Discs/Disc 2/01 - Disc Two Track One.flac
```

## Regenerating it

```sh
scripts/make-fixture-library.sh
```

It needs Docker with Compose v2 and network access (the MusicLib and PostgreSQL images, the Debian snapshot). It starts MusicLib in the Compose project `vibrance-spike` with new volumes and random passwords, creates the input albums, imports them through MusicLib's API, replaces `testdata/library-v1/` and this file, and then deletes the whole project. Every run gives new `album_id` and `build_id` values (MusicLib creates them), so regenerate only on purpose, together with the tests that name them.
