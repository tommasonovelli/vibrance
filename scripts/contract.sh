#!/usr/bin/env bash
# The contract suite with the real MusicLib (DESIGN.md §12.3, step S23): the
# scenarios A1–A20, with the published MusicLib that the Dockerfile pins and
# the runtime image of Vibrance built from these sources.
#
# Usage: scripts/contract.sh
#
# Everything runs in the Compose project `vibrance-contract`, never in
# `musiclib` (a real installation): the script checks the name, the
# container names and that no port is published on the host before it
# starts anything, and it deletes that project, with its volumes, at the
# start (leftovers) and at the end. The stack is the compose.yaml and
# env.example a user downloads, in a private temporary folder, with a .env
# of random passwords that are never printed; an override removes the ports
# and adds the `suite` service. The import folder holds copies of the albums
# of testdata/library-v1/.
#
# The suite is the Go test of internal/contract (build tag `contract`). It
# runs in the `suite` container on the stack's network, changes MusicLib
# through its API and checks Vibrance through its own. What only Docker can
# do (stop MusicLib, its offline rebuild, restarts, hiding library/) it asks
# of this script: a line `@@contract-action <n> <action> [arg]` on its
# output, answered with a file /tmp/contract-control/<n> ("ok [text]" or
# "error text") that this script writes into the container.
#
# It needs network access (MusicLib's images) and takes a few minutes.
# It exits 0 only if every scenario passed.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"
# shellcheck source=scripts/lib/stack.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/stack.sh"

[[ $# -eq 0 ]] || die "usage: scripts/contract.sh (no arguments)"

readonly PROJECT=vibrance-contract
readonly VIBRANCE_IMAGE=vibrance-contract:local
readonly SUITE_IMAGE=vibrance-contract-suite:local
readonly SUITE=vibrance-contract-suite
readonly REBUILD=vibrance-contract-rebuild
readonly CONTROL=/tmp/contract-control
readonly WAIT=(--wait --wait-timeout 300)
# The albums of the fixture library the suite imports, by the name of their
# folder in the import folder.
readonly ALBUMS=(
  "alpha:Aurora Sines/Alpha_ Light_"
  "beta:Bravo Tones/Beta MP3"
  "gamma:Charlie Waves/Gamma AAC"
  "delta:Delta Pulse/Delta ALAC"
  "epsilon:Écho Café/Epsilon Discs"
  "phi:Foxtrot Twins/Phi Same Audio"
)

TMP=""
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
# with its .env and the override of the suite.
stack() {
  docker compose --project-name "${PROJECT}" --project-directory "${STACK}" --env-file "${STACK}/.env" \
    -f "${STACK}/compose.yaml" -f "${STACK}/contract.yaml" --profile suite "$@"
}

contract_end() {
  local rc=$?
  if [[ -n "${STACK}" ]]; then
    if [[ "${rc}" != 0 ]]; then
      info "failed: the last lines of the logs"
      stack logs --tail=40 --no-color app vibrance >&2 </dev/null || true
    fi
    info "deleting the Compose project ${PROJECT} (containers and volumes)"
    docker rm -f "${REBUILD}" >/dev/null 2>&1 </dev/null || true
    stack down --volumes --remove-orphans --timeout 30 >/dev/null 2>&1 </dev/null \
      || printf '%s: warning: could not delete the project %s; run: docker compose -p %s down -v\n' \
        "${0##*/}" "${PROJECT}" "${PROJECT}" >&2
  fi
  if [[ "${BUILT}" == 1 ]]; then
    docker image rm "${VIBRANCE_IMAGE}" "${SUITE_IMAGE}" >/dev/null 2>&1 </dev/null \
      || printf '%s: warning: could not remove the images %s and %s\n' "${0##*/}" "${VIBRANCE_IMAGE}" "${SUITE_IMAGE}" >&2
  fi
  [[ -z "${TMP}" ]] || rm -rf -- "${TMP}"
  exit "${rc}"
}

# 64 hex digits from /dev/urandom.
random_hex() {
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

# Sets NAME=value in .env, as the guide's sed commands do; the line must
# exist.
set_env() {
  grep -q "^$1=" "${TMP}/stack/.env" || die ".env has no line $1="
  sed -i "s|^$1=.*|$1=$2|" "${TMP}/stack/.env"
}

# The install folder: the user's files, the albums to import, a .env with
# three random passwords and the origins the suite reaches the products at,
# and the override of the suite.
prepare() {
  local dir="${TMP}/stack" entry name source
  mkdir -p "${dir}/import"
  cp -- "${REPO_ROOT}/compose.yaml" "${REPO_ROOT}/env.example" "${dir}/"
  for entry in "${ALBUMS[@]}"; do
    name="${entry%%:*}"
    source="${REPO_ROOT}/testdata/library-v1/${entry#*:}"
    # The album as MusicLib wrote it, without its receipt and its extra
    # files: the input of a new import.
    (cd -- "${source}" && find . -path ./Extras -prune -o -type f ! -name .musiclib.json -print) |
      while IFS= read -r file; do
        mkdir -p "${dir}/import/${name}/$(dirname -- "${file}")"
        cp -- "${source}/${file}" "${dir}/import/${name}/${file}"
      done
  done
  chmod -R a+rX "${dir}/import"
  (umask 077 && cp -- "${dir}/env.example" "${dir}/.env")
  set_env POSTGRES_PASSWORD "$(random_hex)"
  set_env MUSICLIB_PASSWORD "$(random_hex)"
  set_env VIBRANCE_ADMIN_PASSWORD "$(random_hex)"
  # The suite reaches both products by their service names. Vibrance scans
  # when the suite asks: no scan of the interval gets in the way.
  set_env PUBLIC_ORIGIN "http://app:8080"
  set_env VIBRANCE_PUBLIC_ORIGIN "http://vibrance:8080"
  printf 'VIBRANCE_SCAN_INTERVAL=24h\n' >>"${dir}/.env"

  cat >"${dir}/contract.yaml" <<EOF
# scripts/contract.sh: no port on the host, the Vibrance image built from
# the sources, and the suite.
services:
  app:
    ports: !reset []
  vibrance:
    image: ${VIBRANCE_IMAGE}
    pull_policy: never
    ports: !reset []
  suite:
    profiles: [suite]
    image: ${SUITE_IMAGE}
    pull_policy: never
    init: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    entrypoint: ["sh", "-c"]
    command:
      - go test -tags contract -c -o /tmp/contract.test ./internal/contract && exec /tmp/contract.test -test.v -test.count=1 -test.timeout=90m
    environment:
      MUSICLIB_URL: http://app:8080
      MUSICLIB_PASSWORD: \${MUSICLIB_PASSWORD:?}
      VIBRANCE_URL: http://vibrance:8080
      VIBRANCE_ADMIN_USERNAME: \${VIBRANCE_ADMIN_USERNAME:-admin}
      VIBRANCE_ADMIN_PASSWORD: \${VIBRANCE_ADMIN_PASSWORD:?}
      CONTRACT_CONTROL: ${CONTROL}
      CONTRACT_SEED: \${CONTRACT_SEED:?}
    volumes:
      # MusicLib's library, read-only: the suite waits for MusicLib to
      # publish a change before it asks Vibrance for a scan.
      - \${MUSICLIB_DATA:-data}:/musiclib:ro
EOF
  STACK="$(native "${dir}")"
}

# Refuses to go on unless the Compose files are the project
# vibrance-contract and touch nothing outside it (check_stack_model). The
# interpolated model holds the passwords: it is written in the private
# temporary folder and only read there.
check_project() {
  local model="${TMP}/model.yaml"
  (umask 077 && stack config >"${model}" </dev/null) || die "docker compose config fails"
  check_stack_model "${model}" "${PROJECT}" "${STACK}"
}

# answer N TEXT: writes the answer to the action N into the suite's
# container.
answer() {
  # shellcheck disable=SC2016 # $1 is expanded by the container's sh
  printf '%s\n' "$2" | docker exec -i "${SUITE}" sh -c 'cat >"$1.tmp" && mv "$1.tmp" "$1"' sh "${CONTROL}/$1" \
    || die "cannot answer the action $1 of the suite"
}

# wait_for FILE PATTERN PID SECONDS: waits until a line of FILE matches
# PATTERN, or the process PID has ended, or the time is over. True only for
# the line.
wait_for() {
  local i
  for ((i = 0; i < $4 * 5; i++)); do
    grep -q "$2" "$1" && return 0
    kill -0 "$3" 2>/dev/null || { grep -q "$2" "$1"; return; }
    sleep 0.2
  done
  return 1
}

WATCH_PID=""
REBUILD_PID=""

# act N ACTION [ARG]: does what the suite asked, and answers it.
act() {
  local n="$1" action="$2" arg="${3:-}" out
  info "action ${n}: ${action} ${arg}"
  case "${action}" in
    vibrance-watch)
      [[ "${arg}" == KILL || "${arg}" == TERM ]] || { answer "${n}" "error signal '${arg}'"; return; }
      : >"${TMP}/watch.log"
      stack exec -T vibrance sh -s -- "${arg}" <"${REPO_ROOT}/docker/contract-watch.sh" >"${TMP}/watch.log" 2>&1 &
      WATCH_PID=$!
      if wait_for "${TMP}/watch.log" '^watching$' "${WATCH_PID}" 60; then
        answer "${n}" ok
      else
        answer "${n}" "error the watcher did not start: $(tr '\n' ' ' <"${TMP}/watch.log")"
      fi
      ;;
    vibrance-watch-result)
      # The exec ends with the container when the signal stops the server.
      wait "${WATCH_PID}" || true
      out="$(grep -m1 '^signal \|^no scan$' "${TMP}/watch.log" || true)"
      if [[ -n "${out}" ]] && ! grep -q '^error ' "${TMP}/watch.log"; then
        answer "${n}" "ok ${out}"
      else
        answer "${n}" "error $(tr '\n' ' ' <"${TMP}/watch.log")"
      fi
      ;;
    restart)
      if stack restart "${arg}" </dev/null >&2; then answer "${n}" ok; else answer "${n}" "error docker compose restart ${arg} failed"; fi
      ;;
    wait-healthy)
      if stack up -d "${WAIT[@]}" app vibrance </dev/null >&2; then answer "${n}" ok; else answer "${n}" "error the stack is not healthy"; fi
      ;;
    rebuild-freeze)
      # MusicLib's guide: the app stopped, `rebuild --store-id` in a one-off
      # container of the app service, the app started again.
      stack stop app </dev/null >&2 || { answer "${n}" "error docker compose stop app failed"; return; }
      : >"${TMP}/rebuild.log"
      stack run --rm --no-deps -T --name "${REBUILD}" --entrypoint sh app -s <"${REPO_ROOT}/docker/contract-rebuild.sh" \
        >"${TMP}/rebuild.log" 2>&1 &
      REBUILD_PID=$!
      if wait_for "${TMP}/rebuild.log" '^frozen$' "${REBUILD_PID}" 120; then
        answer "${n}" ok
      else
        answer "${n}" "error the rebuild was not held: $(tr '\n' ' ' <"${TMP}/rebuild.log")"
      fi
      ;;
    rebuild-continue)
      docker exec "${REBUILD}" touch /tmp/continue </dev/null || { answer "${n}" "error cannot continue the rebuild"; return; }
      if wait "${REBUILD_PID}"; then
        answer "${n}" "ok $(grep -m1 '^rebuild exit' "${TMP}/rebuild.log")"
      else
        answer "${n}" "error the rebuild failed: $(tr '\n' ' ' <"${TMP}/rebuild.log")"
      fi
      ;;
    start-app)
      if stack up -d "${WAIT[@]}" app </dev/null >&2; then answer "${n}" ok; else answer "${n}" "error MusicLib did not start"; fi
      ;;
    hide-library | show-library)
      local from=/data/library to=/data/library.contract-hidden
      [[ "${action}" == hide-library ]] || { from="${to}" to=/data/library; }
      if stack exec -T app mv "${from}" "${to}" </dev/null >&2; then answer "${n}" ok; else answer "${n}" "error mv ${from} ${to} failed"; fi
      ;;
    *)
      answer "${n}" "error unknown action ${action}"
      ;;
  esac
}

# Prints the suite's output and does the actions it asks for. Every command
# here reads /dev/null or a file: standard input is the suite's output.
dispatch() {
  local line n action arg
  while IFS= read -r line; do
    line="${line%$'\r'}"
    if [[ "${line}" == "@@contract-action "* ]]; then
      read -r _ n action arg <<<"${line}"
      act "${n}" "${action}" "${arg}"
    else
      printf '%s\n' "${line}"
    fi
  done
}

main() {
  TMP="$(mktemp -d)"
  chmod 700 "${TMP}"
  trap contract_end EXIT
  trap 'exit 130' INT TERM

  prepare
  export CONTRACT_SEED="${CONTRACT_SEED:-$((RANDOM * 32768 + RANDOM))}"
  check_project

  info "deleting any leftover of the Compose project ${PROJECT}"
  stack down --volumes --remove-orphans --timeout 30 </dev/null || die "cannot delete the old ${PROJECT} project"
  BUILT=1
  info "building the runtime image of Vibrance as ${VIBRANCE_IMAGE}"
  docker build --target runtime -t "${VIBRANCE_IMAGE}" "${REPO_ROOT}" </dev/null || die "building the runtime image failed"
  info "building the image of the suite as ${SUITE_IMAGE}"
  docker build --target test -t "${SUITE_IMAGE}" \
    --build-arg "DEV_UID=${VIBRANCE_DEV_UID}" --build-arg "DEV_GID=${VIBRANCE_DEV_GID}" "${REPO_ROOT}" </dev/null \
    || die "building the image of the suite failed"

  info "starting MusicLib and Vibrance on new volumes (seed ${CONTRACT_SEED})"
  stack up -d "${WAIT[@]}" app vibrance </dev/null || die "the stack did not start"

  info "running the scenarios"
  local rc=0
  stack run --rm --no-deps -T --name "${SUITE}" suite </dev/null 2>&1 | dispatch || rc=$?
  ((rc == 0)) || die "the contract suite failed"
  info "the contract suite passed"
}

main "$@"
