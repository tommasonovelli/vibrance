#!/usr/bin/env bash
# The smoke test of the Compose stack (DESIGN.md S22): the compose.yaml,
# env.example, compose.caddy.yaml and Caddyfile.example a user downloads, run
# for real with MusicLib's published image and the runtime image of Vibrance
# built from these sources.
#
# Usage: scripts/stack-smoke.sh
#
# Everything runs in the Compose project `vibrance-contract`, never in
# `musiclib` (a real installation): the script checks the name, and that no
# container name and no published port can touch another project, before
# it starts anything. It deletes that project with its volumes at the start
# (leftovers) and at the end. Nothing is published on the host: an override
# removes the ports of the services with `!reset`, and a `probe` container
# on the stack's network makes the HTTP requests. The install folder is a
# private temporary folder: compose.yaml, env.example and the rest copied
# from the repository, and a `.env` made the way the install block of
# docs/operations.md makes it, with random passwords that are never printed.
# It needs network access: MusicLib's images, its release files, Caddy's
# plugin.
#
# What it checks:
#   - `docker compose config` accepts the stack alone and with Caddy, and
#     compose.caddy.yaml removes the ports of the two products;
#   - scripts/check-compose-sync.sh is green, and red on a copy with a line
#     of a MusicLib service changed, and on one with a block of Vibrance that
#     changes a MusicLib service;
#   - an installation from scratch in both orders, Vibrance first (it creates
#     MusicLib's data volume) and MusicLib first: MusicLib starts without
#     `volume_permission`, Vibrance lists an album after MusicLib's first
#     import, `docker compose stop app` changes neither the readiness nor the
#     service of Vibrance (and starting it again does not restart Vibrance),
#     and a backup of Vibrance works;
#   - Caddy built from compose.caddy.yaml, with Caddyfile.example turned to
#     `tls internal` on names under example.localhost: both products answer
#     through their name over verified HTTPS, the ETag stays strong, and
#     another name is refused.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"
# shellcheck source=scripts/lib/stack.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/stack.sh"

readonly PROJECT=vibrance-contract
readonly SMOKE_IMAGE=vibrance-stack-smoke:local
readonly ALBUM_SOURCE="testdata/library-v1/Bravo Tones/Beta MP3"
readonly ALBUM_TITLE="Beta MP3"
readonly DOMAIN=example.localhost
readonly WAIT=(--wait --wait-timeout 300)

GO_IMAGE="$(sed -n 's/^ARG GO_IMAGE=//p' "${REPO_ROOT}/Dockerfile")"
[[ "${GO_IMAGE}" == *@sha256:* ]] || die "cannot read ARG GO_IMAGE from the Dockerfile"
readonly GO_IMAGE

SMOKE_TMP=""
STACK=""
BUILT=0

# The install folder in the form docker.exe understands (DESIGN.md T22).
native() {
  case "${OSTYPE:-}" in
    msys* | cygwin*) (cd -- "$1" && pwd -W) ;;
    *) printf '%s\n' "$1" ;;
  esac
}

# docker compose on the project vibrance-contract in the install folder,
# with its .env. The first arguments name the Compose files.
stack() {
  docker compose --project-name "${PROJECT}" --project-directory "${STACK}" \
    --env-file "${STACK}/.env" "$@"
}
plain() { stack -f "${STACK}/compose.yaml" "$@"; }
base() { stack -f "${STACK}/compose.yaml" -f "${STACK}/smoke.yaml" "$@"; }
caddy() {
  stack -f "${STACK}/compose.yaml" -f "${STACK}/compose.caddy.yaml" \
    -f "${STACK}/smoke.yaml" -f "${STACK}/smoke-caddy.yaml" "$@"
}

smoke_end() {
  local rc=$?
  if [[ -n "${STACK}" ]]; then
    if [[ "${rc}" != 0 ]]; then
      info "failed: the last lines of the logs"
      caddy logs --tail=30 --no-color >&2 || true
    fi
    info "deleting the Compose project ${PROJECT} (containers, volumes, the image of Caddy)"
    caddy --profile probe down --volumes --remove-orphans --rmi local --timeout 30 >/dev/null 2>&1 \
      || printf '%s: warning: could not delete the project %s; run: docker compose -p %s down -v\n' \
        "${0##*/}" "${PROJECT}" "${PROJECT}" >&2
  fi
  if [[ "${BUILT}" == 1 ]]; then
    docker image rm "${SMOKE_IMAGE}" >/dev/null 2>&1 \
      || printf '%s: warning: could not remove the image %s\n' "${0##*/}" "${SMOKE_IMAGE}" >&2
  fi
  [[ -z "${SMOKE_TMP}" ]] || rm -rf -- "${SMOKE_TMP}"
  exit "${rc}"
}

# 64 hex digits from /dev/urandom.
random_hex() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

# Sets NAME=value in .env, as the guide's sed commands do; the line must
# exist.
set_env() {
  grep -q "^$1=" "${SMOKE_TMP}/stack/.env" || die ".env has no line $1="
  sed -i "s|^$1=.*|$1=$2|" "${SMOKE_TMP}/stack/.env"
}

# The install folder: the user's files, the album to import, a .env with
# three random passwords, and the two overrides of the test.
prepare() {
  local dir="${SMOKE_TMP}/stack"
  mkdir -p "${dir}/import/${ALBUM_TITLE}"
  cp -- "${REPO_ROOT}/compose.yaml" "${REPO_ROOT}/env.example" "${REPO_ROOT}/compose.caddy.yaml" \
    "${REPO_ROOT}/Caddyfile.example" "${REPO_ROOT}/docker/stack-probe.sh" "${dir}/"
  cp -- "${REPO_ROOT}/${ALBUM_SOURCE}"/*.mp3 "${dir}/import/${ALBUM_TITLE}/"
  chmod -R a+rX "${dir}/import" "${dir}/stack-probe.sh"
  (umask 077 && cp -- "${dir}/env.example" "${dir}/.env")
  set_env POSTGRES_PASSWORD "$(random_hex)"
  set_env MUSICLIB_PASSWORD "$(random_hex)"
  set_env VIBRANCE_ADMIN_PASSWORD "$(random_hex)"

  cat >"${dir}/smoke.yaml" <<EOF
# scripts/stack-smoke.sh: no port on the host, the Vibrance image built from
# the sources, and the probe that makes the HTTP requests.
services:
  app:
    ports: !reset []
  vibrance:
    image: ${SMOKE_IMAGE}
    pull_policy: never
    ports: !reset []
  probe:
    profiles: [probe]
    image: ${GO_IMAGE}
    read_only: true
    tmpfs: [/tmp]
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    entrypoint: ["bash", "/probe.sh"]
    environment:
      VIBRANCE_URL: \${VIBRANCE_PUBLIC_ORIGIN:?}
      MUSICLIB_URL: \${PUBLIC_ORIGIN:?}
      VIBRANCE_ADMIN_USERNAME: \${VIBRANCE_ADMIN_USERNAME:-admin}
      VIBRANCE_ADMIN_PASSWORD: \${VIBRANCE_ADMIN_PASSWORD:?}
      MUSICLIB_PASSWORD: \${MUSICLIB_PASSWORD:?}
    volumes:
      - ./stack-probe.sh:/probe.sh:ro
EOF
  cat >"${dir}/smoke-caddy.yaml" <<'EOF'
# scripts/stack-smoke.sh with Caddy: no port on the host, and the probe
# trusts Caddy's local certificate authority.
services:
  caddy:
    ports: !reset []
  probe:
    volumes:
      - caddy_data:/caddy:ro
EOF
  STACK="$(native "${dir}")"
}

# Refuses to go on unless every Compose file set is the project
# vibrance-contract and touches nothing outside it (check_stack_model). The
# interpolated model holds the passwords: it is written in the private
# temporary folder and only read there.
check_project() {
  local set model
  for set in base caddy; do
    model="${SMOKE_TMP}/model-${set}.yaml"
    (umask 077 && "${set}" config >"${model}") || die "docker compose config fails (${set})"
    check_stack_model "${model}" "${PROJECT}" "${STACK}"
  done
}

# Prints the service $2 of the model in file $1.
service_of() {
  awk -v svc="  $2:" '/^[^ ]/ { s = ($0 == "services:") } s && /^  [^ ]/ { in_svc = ($0 == svc) } in_svc' "$1"
}

check_config() {
  local model="${SMOKE_TMP}/model-user.yaml"
  info "docker compose config: the stack, and the stack with Caddy"
  plain config --quiet || die "docker compose config refuses compose.yaml"
  (umask 077 && stack -f "${STACK}/compose.yaml" -f "${STACK}/compose.caddy.yaml" config >"${model}") \
    || die "docker compose config refuses compose.yaml with compose.caddy.yaml"
  for svc in app vibrance; do
    if service_of "${model}" "${svc}" | grep '^    ports:' >/dev/null; then
      die "with compose.caddy.yaml the service ${svc} still publishes a port"
    fi
  done
  service_of "${model}" caddy | grep '^    ports:' >/dev/null || die "compose.caddy.yaml publishes no port for Caddy"
}

check_sync() {
  local copy="${SMOKE_TMP}/altered"
  info "check-compose-sync.sh: green on the files as released"
  "${REPO_ROOT}/scripts/check-compose-sync.sh" "${SMOKE_TMP}/stack" || die "the sync check fails on the released files"

  mkdir -p "${copy}"
  cp -- "${REPO_ROOT}/env.example" "${copy}/"
  sed 's/^    stop_grace_period: 45s$/    stop_grace_period: 46s/' "${REPO_ROOT}/compose.yaml" >"${copy}/compose.yaml"
  ! cmp -s "${REPO_ROOT}/compose.yaml" "${copy}/compose.yaml" || die "the alteration of the app service did not apply"
  info "check-compose-sync.sh: must be red with a line of MusicLib's app service changed"
  if "${REPO_ROOT}/scripts/check-compose-sync.sh" "${copy}" >/dev/null 2>&1; then
    die "the sync check is green on a compose.yaml with a line of MusicLib changed"
  fi

  sed 's/^    stop_grace_period: 45s$/    stop_grace_period: 45s\n    # >>> Vibrance\n    cap_add: [CHOWN]\n    # <<< Vibrance/' \
    "${REPO_ROOT}/compose.yaml" >"${copy}/compose.yaml"
  ! cmp -s "${REPO_ROOT}/compose.yaml" "${copy}/compose.yaml" || die "the block inside the app service did not apply"
  info "check-compose-sync.sh: must be red with a block of Vibrance inside MusicLib's app service"
  if "${REPO_ROOT}/scripts/check-compose-sync.sh" "${copy}" >/dev/null 2>&1; then
    die "the sync check is green on a compose.yaml whose block changes MusicLib's app service"
  fi
}

# The probe: $1 says how to reach the products, http (the default origins,
# straight to the services) or https (through Caddy); the rest is the
# probe's command.
probe() {
  local how="$1" args=()
  shift
  case "${how}" in
    http) args=(-e "VIBRANCE_CONNECT=127.0.0.1:8090:vibrance:8080" -e "MUSICLIB_CONNECT=127.0.0.1:8080:app:8080") ;;
    https) args=(-e "VIBRANCE_CONNECT=vibrance.${DOMAIN}:443:caddy:443" -e "MUSICLIB_CONNECT=musiclib.${DOMAIN}:443:caddy:443"
      -e "CACERT=/caddy/caddy/pki/authorities/local/root.crt") ;;
  esac
  if [[ "${how}" == https ]]; then
    caddy --profile probe run --rm --no-deps -T "${args[@]}" probe "$@"
  else
    base --profile probe run --rm --no-deps -T "${args[@]}" probe "$@"
  fi
}

state_of() {
  docker inspect --format "{{.State.StartedAt}} {{.RestartCount}} {{.State.Health.Status}}" "${PROJECT}-vibrance"
}

# What both orders check once the whole stack runs.
check_running_stack() {
  local order="$1" before after
  base logs app >"${SMOKE_TMP}/app.log" 2>&1 || die "cannot read the log of MusicLib"
  if grep -q volume_permission "${SMOKE_TMP}/app.log"; then
    die "MusicLib logged volume_permission"
  fi
  probe http musiclib-ready || die "MusicLib is not ready"
  probe http import || die "the import into MusicLib failed"
  probe http vibrance-album "${ALBUM_TITLE}" || die "Vibrance does not see MusicLib's library"

  info "docker compose stop app: Vibrance must keep running, ready, with its index"
  before="$(state_of)"
  base stop app || die "docker compose stop app failed"
  probe http vibrance-ready || die "Vibrance is not ready with MusicLib stopped"
  probe http vibrance-album "${ALBUM_TITLE}" || die "Vibrance lost the album with MusicLib stopped"
  after="$(state_of)"
  [[ "${after}" == "${before}" && "${after}" == *" 0 healthy" ]] \
    || die "Vibrance changed when MusicLib stopped: '${before}' became '${after}'"
  info "docker compose up -d --wait: MusicLib starts again, Vibrance is not restarted"
  base up -d "${WAIT[@]}" || die "the stack did not start again"
  after="$(state_of)"
  [[ "${after}" == "${before}" ]] || die "Vibrance changed when MusicLib started: '${before}' became '${after}'"

  info "a backup of Vibrance, as in docs/operations.md"
  base exec -T vibrance vibrance backup --to "/backup/smoke-${order}" || die "the backup of Vibrance failed"
}

# Vibrance first: its container creates MusicLib's data volume, which must
# then belong to MusicLib's user (DESIGN.md T21).
order_vibrance_first() {
  local owner
  info "order 1: Vibrance first, alone, on new volumes"
  base up -d "${WAIT[@]}" --no-deps vibrance || die "Vibrance did not become healthy without MusicLib"
  probe http vibrance-ready || die "Vibrance is not ready without MusicLib"
  owner="$(base run --rm --no-deps -T --entrypoint stat vibrance -c %u:%g /musiclib)" || die "cannot read the owner of /musiclib"
  [[ "${owner}" == 1000:1000 ]] || die "the data volume created by Vibrance belongs to ${owner}, not 1000:1000"
  info "order 1: then MusicLib"
  base up -d "${WAIT[@]}" || die "MusicLib did not start on the data volume Vibrance created"
  check_running_stack vibrance-first
}

order_musiclib_first() {
  info "order 2: MusicLib first, on new volumes"
  base up -d "${WAIT[@]}" app || die "MusicLib did not become healthy"
  info "order 2: then Vibrance"
  base up -d "${WAIT[@]}" || die "Vibrance did not become healthy"
  probe http vibrance-ready || die "Vibrance is not ready"
  check_running_stack musiclib-first
}

# Caddy as compose.caddy.yaml builds it, with Caddyfile.example made local:
# its names under example.localhost and `tls internal` instead of the DNS
# challenge.
with_caddy() {
  local file="${SMOKE_TMP}/stack/Caddyfile"
  # Written first: a container that mounts ./Caddyfile before it exists
  # would make it a folder.
  awk '
    /^\ttls \{$/ { print "\ttls internal"; skip = 1; next }
    skip && /^\t\}$/ { skip = 0; next }
    !skip { gsub(/example\.com/, "example.localhost"); print }' \
    "${REPO_ROOT}/Caddyfile.example" >"${file}"
  if ! grep -q '^	tls internal$' "${file}" || grep -q 'cloudflare\|example\.com' "${file}"; then
    die "cannot turn Caddyfile.example to tls internal"
  fi
  info "Caddy: building the image of compose.caddy.yaml"
  caddy build caddy || die "building Caddy failed"
  caddy run --rm --no-deps -T --entrypoint caddy caddy adapt --adapter caddyfile --config /dev/stdin \
    <"${REPO_ROOT}/Caddyfile.example" >/dev/null || die "Caddy refuses Caddyfile.example"
  set_env PUBLIC_ORIGIN "https://musiclib.${DOMAIN}"
  set_env VIBRANCE_PUBLIC_ORIGIN "https://vibrance.${DOMAIN}"
  check_project
  info "Caddy: the stack with compose.caddy.yaml, names under ${DOMAIN}, tls internal"
  caddy up -d "${WAIT[@]}" || die "the stack with Caddy did not start"
  probe https vibrance-ready || die "Vibrance does not answer through Caddy"
  probe https musiclib-ready || die "MusicLib does not answer through Caddy"
  probe https vibrance-album "${ALBUM_TITLE}" || die "Vibrance does not serve its index through Caddy"
  probe https strong-etag || die "the ETag is not strong through Caddy"
  probe https refused "other.${DOMAIN}" || die "Caddy answers a name it does not route"
}

main() {
  SMOKE_TMP="$(mktemp -d)"
  chmod 700 "${SMOKE_TMP}"
  trap smoke_end EXIT
  trap 'exit 130' INT TERM

  prepare
  check_project
  check_config
  check_sync

  info "deleting any leftover of the Compose project ${PROJECT}"
  caddy --profile probe down --volumes --remove-orphans --timeout 30 || die "cannot delete the old ${PROJECT} project"
  info "building the runtime image of Vibrance as ${SMOKE_IMAGE}"
  BUILT=1
  docker build --target runtime -t "${SMOKE_IMAGE}" "${REPO_ROOT}" || die "building the runtime image failed"

  order_vibrance_first
  info "deleting the project's containers and volumes before the other order"
  base --profile probe down --volumes --remove-orphans --timeout 30 || die "cannot delete the project"
  order_musiclib_first
  with_caddy
  info "the stack smoke test passed"
}

main "$@"
