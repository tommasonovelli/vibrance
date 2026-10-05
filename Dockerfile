# syntax=docker/dockerfile:1.26.0@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
#
# Vibrance: one multi-stage Dockerfile for the toolchain, the test gate and
# the runtime image (DESIGN.md §3.6, §11.6).
#
# Every base image is pinned by exact version AND by the digest of its
# multi-arch index (DESIGN.md I12: never `latest`). The Go, Debian and
# Dockerfile-frontend pins are copied from Vibrance MusicLib 1.2.0 (its
# Dockerfile and docs/docker.md, "Pinned images"). To bump one, resolve the
# new digest with `docker buildx imagetools inspect <image>:<exact-tag>`.
#
# Targets:
#   media-tools  ffmpeg + ffprobe, the static binaries of the pinned MusicLib image
#   toolchain    Go compiler + gcc (race detector), ffmpeg, ffprobe, non-root `dev` user
#   deps         toolchain + module cache downloaded from go.mod/go.sum
#   test         deps + a read-only snapshot of the source tree (scripts/check.sh)
#   build-app    compiles ./cmd/vibrance (static, CGO_ENABLED=0), stamped with VIBRANCE_VERSION
#   runtime      the image of the `vibrance` service (DESIGN.md §11.6)

ARG GO_IMAGE=golang:1.25.14-trixie@sha256:2c4c60ef415fbfa5e90300722293bef36c5e63fae17570ce18f580af933dbd73
ARG RUNTIME_IMAGE=debian:trixie-20260918-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a
# The published MusicLib release whose ffmpeg and ffprobe Vibrance uses
# (DESIGN.md D18): 8.1.3-musiclib1, fully static, the same bytes everywhere.
ARG MUSICLIB_IMAGE=ghcr.io/tommasonovelli/musiclib:1.2.0@sha256:52204bdf0ca23eeae71453f2ae8a1ee0a8d9b52ef4ce58df42ace369ab8012dd

# UID/GID of the unprivileged `dev` user of the toolchain stages. Declared
# here so that every stage that uses them sees the same default; a stage must
# still redeclare them (without a value) to use them.
ARG DEV_UID=10001
ARG DEV_GID=10001

# The application version, a token of [0-9A-Za-z.+-]. Only `build-app` and
# the labels at the end of `runtime` redeclare it, so a new value never
# invalidates the toolchain or test layers.
ARG VIBRANCE_VERSION=devel
# The commit and the repository URL of a release, for the image labels
# (empty by default). Only the end of `runtime` redeclares them.
ARG VIBRANCE_REVISION=
ARG VIBRANCE_SOURCE=

# ---------------------------------------------------------------------------
# ffmpeg and ffprobe (DESIGN.md §3.6, D18): copied, never built, from the
# pinned MusicLib image, where they are /usr/local/bin/ffmpeg and
# /usr/local/bin/ffprobe.
FROM ${MUSICLIB_IMAGE} AS media-tools

# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS toolchain

# UID/GID of the unprivileged user that builds and runs the tests. The scripts
# pass the host user's ids so that bind-mounted sources stay writable on a
# native Docker Engine; running as root would also make permission tests
# meaningless (root bypasses DAC checks).
ARG DEV_UID
ARG DEV_GID

# GOTOOLCHAIN=local: never download a different toolchain behind our back.
# CGO_ENABLED=1: required by `go test -race` (gcc and libc6-dev are in the
# base image). -buildvcs=false: the source snapshot has no .git and bind
# mounts have foreign ownership; the version comes from -ldflags.
ENV GOTOOLCHAIN=local \
    CGO_ENABLED=1 \
    GOFLAGS="-mod=readonly -buildvcs=false" \
    GOPATH=/home/dev/go \
    GOCACHE=/home/dev/.cache/go-build \
    PATH=/usr/local/go/bin:/home/dev/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# The mount points of the named volumes are created here, owned by `dev`: an
# empty named volume inherits owner and mode of the directory it is mounted
# on, so the caches and /testdata are writable without running as root.
RUN groupadd --non-unique --gid "${DEV_GID}" dev \
 && useradd --non-unique --uid "${DEV_UID}" --gid "${DEV_GID}" \
      --create-home --home-dir /home/dev --shell /bin/bash dev \
 && install -d -o dev -g dev -m 0755 \
      /home/dev/go /home/dev/go/pkg /home/dev/go/pkg/mod /home/dev/.cache /home/dev/.cache/go-build \
      /testdata /src

# The same ffmpeg/ffprobe binaries as `runtime`: the tests run the real tools
# (DESIGN.md §12.1), and TestPinnedToolsInstalled checks their version.
COPY --from=media-tools /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/
WORKDIR /src
USER dev

# ---------------------------------------------------------------------------
FROM toolchain AS deps

# Only go.mod/go.sum: this layer is rebuilt only when dependencies change.
# `go mod verify` checks the downloaded modules against go.sum. go.sum does
# not exist while the module has no dependencies (NOTES.md N-003).
COPY go.mod go.sum* ./
RUN go mod download && go mod verify

# ---------------------------------------------------------------------------
FROM deps AS test

# Source snapshot owned by root and therefore read-only for `dev`: the gate
# tests exactly the tree that was in the build context, and a test cannot
# modify the sources. Tests write only to TMPDIR (/testdata, ext4 volume) and
# GOCACHE (named volume); see docker/with-testdata.sh.
COPY --chown=root:root . .

ENTRYPOINT ["/src/docker/with-testdata.sh"]
CMD ["/src/docker/gate.sh"]

# ---------------------------------------------------------------------------
FROM deps AS build-app

# Needed by the cache mount below: a stage sees only the ARGs it declares.
ARG DEV_UID
ARG DEV_GID
COPY . .
# Static binary, -trimpath + pinned toolchain + no VCS stamping: the same
# sources and VIBRANCE_VERSION give the same bytes. The version must be a
# non-empty token, and the built binary must report it: a mistyped -X path
# would silently leave "devel".
ARG VIBRANCE_VERSION
RUN --mount=type=cache,target=/home/dev/.cache/go-build,uid=${DEV_UID},gid=${DEV_GID} \
    case "${VIBRANCE_VERSION}" in \
      ''|*[!0-9A-Za-z.+-]*) echo "VIBRANCE_VERSION must be a non-empty token of [0-9A-Za-z.+-]: '${VIBRANCE_VERSION}'" >&2; exit 1 ;; \
    esac \
 && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X vibrance/internal/buildinfo.Version=${VIBRANCE_VERSION}" \
      -o /home/dev/out/vibrance ./cmd/vibrance \
 && test "$(/home/dev/out/vibrance version)" = "version: ${VIBRANCE_VERSION}"

# ---------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

# The image's uid:gid, 1000:1000 (DESIGN.md §11.6). /musiclib (mount point of
# MusicLib's data volume, read-only), /var/lib/vibrance (state) and /backup
# are created owned by it: an empty named volume mounted there inherits that
# owner. A new MusicLib data volume that Vibrance mounts first thus belongs to
# MusicLib's user from the start (DESIGN.md T21; scripts/stack-smoke.sh
# checks it, NOTES.md N-153).
ARG APP_UID=1000
ARG APP_GID=1000

# ffmpeg and ffprobe: static, the same bytes as in the test and dev images.
COPY --from=media-tools /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/

# Vibrance's license and the notices of the third-party software in this
# image (ffmpeg, the Go modules, the script of the documentation page), with
# the license texts they refer to (THIRD_PARTY_NOTICES.md), owned by root.
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/vibrance/
COPY licenses/ /usr/share/doc/vibrance/licenses/

# The texts get files 0644 and folders 0755 here, whatever the modes in the
# checkout: a Windows build context marks every file executable, and COPY
# --chmod would give the folders it creates the files' mode.
RUN install -d -o "${APP_UID}" -g "${APP_GID}" -m 0755 /musiclib /var/lib/vibrance /backup \
 && find /usr/share/doc/vibrance -type d -exec chmod 0755 {} + \
 && find /usr/share/doc/vibrance -type f -exec chmod 0644 {} +

COPY --from=build-app --chown=root:root /home/dev/out/vibrance /usr/local/bin/vibrance

# Never root. Compose overrides this with `user: UID:GID`; this is only the
# fallback for a bare `docker run`.
USER ${APP_UID}:${APP_GID}
# The port of the default VIBRANCE_HTTP_ADDR, :8080.
EXPOSE 8080
WORKDIR /
# The image runs the server; the operational subcommands replace the
# command: `docker compose run --rm --no-deps vibrance restore --from ...`,
# `docker compose exec vibrance vibrance backup --to ...` (docs/operations.md).
ENTRYPOINT ["/usr/local/bin/vibrance"]
CMD ["serve"]

# OCI annotations of the published image, last so that new values change no
# layer (NOTES.md N-148, N-166). The version is the one stamped into the
# binary; the release workflow passes the commit and the repository URL.
ARG VIBRANCE_VERSION
ARG VIBRANCE_REVISION
ARG VIBRANCE_SOURCE
LABEL org.opencontainers.image.title="Vibrance" \
      org.opencontainers.image.description="Self-hosted listening server over the library of Vibrance MusicLib: accounts, favorites, playlists, search and an HTTP API." \
      org.opencontainers.image.version="${VIBRANCE_VERSION}" \
      org.opencontainers.image.revision="${VIBRANCE_REVISION}" \
      org.opencontainers.image.source="${VIBRANCE_SOURCE}" \
      org.opencontainers.image.licenses="MIT"
