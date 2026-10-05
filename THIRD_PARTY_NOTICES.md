# Third-party notices

The container image `ghcr.io/tommasonovelli/vibrance` holds Vibrance and the third-party software listed here. Vibrance's own code is under the MIT License ([LICENSE](LICENSE)). Each third-party component keeps its own license, below.

Paths in this file are relative to the root of the source repository. In the image, this file, `LICENSE` and `licenses/` are under `/usr/share/doc/vibrance/`.

**Sources.** Every GitHub release of Vibrance attaches this file, the unmodified source tarball of FFmpeg that the image's `ffmpeg` and `ffprobe` were built from (checked against the sha256 that MusicLib's `Dockerfile` pins), and that `Dockerfile` of MusicLib, whose `build-ffmpeg` stage is the script that compiled them. Vibrance's own build instructions are its `Dockerfile`, in the release's source code archives. The sources of the Debian packages are in Debian's permanent snapshot archive (see [Debian packages](#debian-packages)).

| Component | Version | License | In the image |
|---|---|---|---|
| [FFmpeg](#ffmpeg) | 8.1.3 (`8.1.3-musiclib1`) | GPL-2.0-or-later as built (FFmpeg is otherwise LGPL-2.1-or-later; some files BSD, MIT, ISC, IJG) | `/usr/local/bin/ffmpeg`, `/usr/local/bin/ffprobe` |
| [GNU C Library](#gnu-c-library-gcc-runtime-library) | 2.41-12+deb13u3 (Debian) | LGPL-2.1-or-later | statically linked into `ffmpeg` and `ffprobe` |
| [GCC runtime library](#gnu-c-library-gcc-runtime-library) (libgcc) | 14.2.0-19 (Debian) | GPL-3.0-or-later with the GCC Runtime Library Exception 3.1 | statically linked into `ffmpeg` and `ffprobe` |
| [Go standard library and runtime](#go) | 1.25.14 | BSD-3-Clause | compiled into `/usr/local/bin/vibrance` |
| [Go modules](#go) | see the table below | BSD-3-Clause, MIT, Apache-2.0, public domain | compiled into `vibrance` |
| [Scalar API Reference](#scalar-api-reference) | 1.72.4 | MIT | embedded in `vibrance`, served to the browser at `/api/docs/scalar.js` |
| [Debian packages](#debian-packages) | Debian 13 (trixie) | per package | the base system |

## FFmpeg

`ffmpeg` and `ffprobe` are not built by Vibrance: the `Dockerfile` copies them, unchanged, from the published image of Vibrance MusicLib 1.2.0 (`ARG MUSICLIB_IMAGE`, pinned by digest), whose `build-ffmpeg` stage compiled them.

- Copyright (c) 2000-2026 the FFmpeg developers; the files under other licenses name their own authors in `licenses/ffmpeg/NOTICES.txt`.
- License: `ffmpeg` and `ffprobe` as distributed in the image are under the **GNU General Public License version 2 or later**. FFmpeg is otherwise under the GNU Lesser General Public License version 2.1 or later, and MusicLib's build passes no `--enable-gpl`, `--enable-version3` or `--enable-nonfree` and links no external library but the C library (`--disable-autodetect`). FFmpeg 8.1.3 nevertheless compiles three files under GPL-2.0-or-later into both binaries with its `perlin` and `codecview` filters, which it enables without `--enable-gpl`: `libavfilter/perlin.c`, `libavfilter/qp_table.c` and `libavfilter/qp_table.h`. The other files keep their own licenses (LGPL and the permissive ones in `NOTICES.txt`), all compatible with the GPL. `ffmpeg -L` prints the LGPL notice because the build does not set `--enable-gpl`; it does not account for these three files. `ffmpeg -buildconf` prints the configuration of the binaries in the image.
- Vibrance's own code is not affected: `vibrance` runs `ffmpeg` and `ffprobe` as separate programs, with command-line arguments, pipes and open files, and is not linked with FFmpeg. It stays under the MIT License.
- This software is based in part on the work of the Independent JPEG Group.
- Texts: `licenses/ffmpeg/COPYING.GPLv2` (the license of the binaries), `licenses/ffmpeg/COPYING.LGPLv2.1`, `licenses/ffmpeg/LICENSE.md` (FFmpeg's licensing summary), `licenses/ffmpeg/NOTICES.txt` (the BSD, MIT, ISC and IJG notices of the files compiled in). They are MusicLib's files of the same binaries, byte for byte.
- Source: `ffmpeg-8.1.3.tar.gz`, the complete FFmpeg source of both binaries, is attached unmodified to every Vibrance release, together with MusicLib's `Dockerfile` (`musiclib-1.2.0-Dockerfile`), whose `build-ffmpeg` stage holds the scripts that control their compilation. Upstream at <https://ffmpeg.org/releases/ffmpeg-8.1.3.tar.gz>; MusicLib's release at <https://github.com/tommasonovelli/vibrance-musiclib/releases/tag/v1.2.0> attaches the same tarball. The parts of the C library and of the GCC runtime statically linked into both binaries come from the Debian source packages named below. The binaries are static; to rebuild them, with a modified FFmpeg or a modified C library, follow the `build-ffmpeg` stage.

## GNU C Library, GCC runtime library

`ffmpeg` and `ffprobe` are fully static C programs: they contain parts of the GNU C Library and of GCC's `libgcc`, taken unmodified from Debian's `glibc` 2.41-12+deb13u3 and `gcc-14` 14.2.0-19 (the packages `libc6-dev` and `libgcc-14-dev` of MusicLib's build image, which is the Go image `ARG GO_IMAGE` of Vibrance's `Dockerfile`, pinned by the same digest).

- **GNU C Library**: Copyright (C) 1991-2025 Free Software Foundation, Inc. and others. License: GNU LGPL version 2.1 or later; a few parts are under other permissive licenses, all listed in Debian's copyright file, which is in the image as `/usr/share/doc/libc6/copyright` (the image's own `libc6` package, 2.41-12+deb13u4, of the same upstream release). LGPL-2.1 text: `/usr/share/common-licenses/LGPL-2.1` in the image, and `licenses/ffmpeg/COPYING.LGPLv2.1`, which differs only in the FSF's address. Source: <https://snapshot.debian.org/package/glibc/2.41-12%2Bdeb13u3/>. The programs that link it are free software with their complete source and build instructions available (above), so you can rebuild and relink them with a modified C library.
- **libgcc**: Copyright (C) Free Software Foundation, Inc. License: GNU GPL version 3 or later with the GCC Runtime Library Exception version 3.1, which allows it to be combined with programs under any license. Text: `/usr/share/doc/gcc-14-base/copyright` in the image (the same package version). Source: <https://snapshot.debian.org/package/gcc-14/14.2.0-19/>.

## Go

`vibrance` is compiled with Go 1.25.14, without cgo (`go version -m /usr/local/bin/vibrance` lists what it contains).

- **Go standard library and runtime**: Copyright 2009 The Go Authors. License: BSD-3-Clause, with Google's patent grant. Texts: `licenses/go/LICENSE`, `licenses/go/PATENTS`. Source: <https://go.dev/dl/go1.25.14.src.tar.gz>.
- **Go modules**, each at the version pinned in `go.sum`; the files each module ships with its license (`LICENSE`, `NOTICE`, `PATENTS`, `AUTHORS`) are under `licenses/go-modules/<module path>/`:

| Module | Version | License | Copyright |
|---|---|---|---|
| github.com/apapsch/go-jsonmerge/v2 | v2.0.0 | MIT | Copyright (c) 2016-2019 Artur Kraev |
| github.com/dustin/go-humanize | v1.0.1 | MIT | Copyright (c) 2005-2008 Dustin Sallings |
| github.com/getkin/kin-openapi | v0.149.0 | MIT | Copyright (c) 2017-2018 the project authors |
| github.com/go-openapi/jsonpointer | v0.23.1 | Apache-2.0 (with a NOTICE file) | Copyright 2015-2025 go-swagger maintainers |
| github.com/go-openapi/swag/jsonname | v0.26.0 | Apache-2.0 | go-swagger maintainers |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | Copyright (c) 2009,2014 Google Inc. |
| github.com/mfridman/interpolate | v0.0.2 | MIT | Copyright (c) 2014-2017 Buildkite Pty Ltd; Copyright (c) 2023 Michael Fridman |
| github.com/oapi-codegen/runtime | v1.7.0 | Apache-2.0 | the oapi-codegen authors (the module has no NOTICE file) |
| github.com/oasdiff/yaml | v0.1.1 | MIT (and BSD-3-Clause for the parts from Go) | Copyright (c) 2014 Sam Ghods |
| github.com/oasdiff/yaml3 | v0.0.14 | MIT and Apache-2.0 (with a NOTICE file) | Copyright (c) 2006-2011 Kirill Simonov; Copyright 2011-2019 Canonical Ltd |
| github.com/pressly/goose/v3 | v3.27.0 | MIT | Original work Copyright (c) 2012 Liam Staskawicz; Modified work Copyright (c) 2016 Vojtech Vitek |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | Copyright (c) 2012 The Go Authors |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | Santhosh Kumar Tekuri (the module has no NOTICE file) |
| github.com/sethvargo/go-retry | v0.3.0 | Apache-2.0 | Seth Vargo (the module has no NOTICE file) |
| go.uber.org/multierr | v1.11.0 | MIT | Copyright (c) 2017-2021 Uber Technologies, Inc. |
| golang.org/x/crypto | v0.55.0 | BSD-3-Clause (+ PATENTS) | Copyright 2009 The Go Authors |
| golang.org/x/image | v0.45.0 | BSD-3-Clause (+ PATENTS) | Copyright 2009 The Go Authors |
| golang.org/x/sync | v0.22.0 | BSD-3-Clause (+ PATENTS) | Copyright 2009 The Go Authors |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause (+ PATENTS) | Copyright 2009 The Go Authors |
| golang.org/x/text | v0.41.0 | BSD-3-Clause (+ PATENTS) | Copyright 2009 The Go Authors |
| modernc.org/libc | v1.75.7 | BSD-3-Clause; the third-party code it carries (from musl, Go and others) is listed with its licenses in `LICENSE-3RD-PARTY.md` | Copyright (c) 2017 The Libc Authors |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause | Copyright (c) 2014 The mathutil Authors |
| modernc.org/memory | v1.12.1 | BSD-3-Clause; parts from Go (`LICENSE-GO`) and mmap-go (`LICENSE-MMAP-GO`, BSD-3-Clause) | Copyright (c) 2017 The Memory Authors |
| modernc.org/sqlite | v1.59.0 | BSD-3-Clause; SQLite itself is in the public domain (`LICENSE-SQLITE`); `LICENSE-SQLITE_VEC` (MIT) is shipped by the module for an extension Vibrance does not use | Copyright (c) 2017 The Sqlite Authors |

The sources of these modules are on <https://proxy.golang.org/> at the versions above.

## Scalar API Reference

- Copyright (c) 2023-present Scalar (<https://github.com/scalar/scalar>).
- License: MIT. Text: `licenses/scalar/LICENSE` (the license of Scalar's repository; the npm package declares `MIT` and carries no license file).
- The file is `web/docs/scalar.js`, the unmodified `package/dist/browser/standalone.js` of the npm package `@scalar/api-reference` 1.72.4; `web/docs/VENDOR.md` records its source and its SHA-256. It is embedded in `vibrance` and sent to the browsers that open `/api/docs`.
- It is a bundle: it also contains the open-source libraries Scalar is built with, whose license comments it keeps (among them Tailwind CSS, MIT, and focus-trap and tabbable, MIT). The full list of those libraries and their licenses is the dependency tree of `@scalar/api-reference` 1.72.4 on <https://www.npmjs.com/>.

## Debian packages

The image is based on `debian:trixie-20260918-slim` (pinned by digest as `RUNTIME_IMAGE` in the `Dockerfile`), built from Debian's archive as of 2026-09-18; Vibrance adds no package to it. Each package's copyright and license terms are in the image at `/usr/share/doc/<package>/copyright` (the slim image keeps these files), and the common license texts in `/usr/share/common-licenses/`. `dpkg-query -W` in the image lists every package with its version; the image's SBOM, attached to it in the registry, lists them too. The exact source of any package version is available from Debian's snapshot archive at `https://snapshot.debian.org/package/<source package>/<version>/`; the archives of the base image are <http://snapshot.debian.org/archive/debian/20260918T000000Z/> and <http://snapshot.debian.org/archive/debian-security/20260918T000000Z/>.

## Not in the image

The fixture library under `testdata/`, sqlc, shellcheck, `oapi-codegen` (a `tool` of `go.mod`, which generates `internal/api` at development time), MusicLib and PostgreSQL (in the Compose stack, from their own images) and Caddy are used only to build, test or run Vibrance next to it: none of them is in Vibrance's image.
