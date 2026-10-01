#!/usr/bin/env bash
# The S1 spike (DESIGN.md §14): checks the hypotheses H1–H9 of the contract
# with MusicLib against the real MusicLib 1.1.0 and writes
# docs/spike-report.md, with every command and its real output.
#
# Usage: scripts/spike.sh
#
# It starts the published MusicLib in the Compose project `vibrance-spike`
# (new volumes, random passwords, no published port), imports the input
# albums of DESIGN.md §12.2, changes them through MusicLib's API, stops
# MusicLib for an offline rebuild, and reads the result as Vibrance will
# (the program in scripts/spike/, in the tools container). Then it deletes
# the whole project. It needs network access (images, the Debian snapshot).
# It exits 1 if a hypothesis is false; the report is written anyway.
set -euo pipefail

# shellcheck source=scripts/lib/common.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/common.sh"
# shellcheck source=scripts/lib/spike.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/lib/spike.sh"

[[ $# -eq 0 ]] || die "usage: scripts/spike.sh (no arguments)"

# record FRAGMENT DISPLAY CMD...: runs CMD, and appends DISPLAY (the command
# as shown in the report) and its output (stdout and stderr) to the
# Markdown file FRAGMENT. Returns the command's exit status.
record() {
  local fragment="$1" display="$2" out rc=0
  shift 2
  out="$("$@" 2>&1)" || rc=$?
  {
    printf '```console\n$ %s\n' "${display}"
    [[ -z "${out}" ]] || printf '%s\n' "${out}"
    [[ ${rc} -eq 0 ]] || printf '(exit status %d)\n' "${rc}"
    printf '```\n\n'
  } >>"${fragment}"
  return "${rc}"
}

# H1: the image can be pulled, and holds the pinned ffmpeg and ffprobe with
# the hash muxer. Written on the host: only the host runs docker.
check_h1() {
  local f="${SPIKE_TMP}/H1.md" ok=true tag="${MUSICLIB_IMAGE%@*}" digest="${MUSICLIB_IMAGE#*@}"
  local run=(docker run --rm --network none)
  cat >"${f}" <<EOF
## H1: the MusicLib image

Claim (\`DESIGN.md\` §3.6, D18): \`${tag}\` can be pulled; it has \`ffmpeg\` and \`ffprobe\` in \`/usr/local/bin\` at version \`8.1.3-musiclib1\`, and the \`hash\` muxer. The pin is the Dockerfile's \`ARG MUSICLIB_IMAGE\`. These commands ran on the host.

EOF
  record "${f}" "docker buildx imagetools inspect ${tag}" docker buildx imagetools inspect "${tag}" || ok=false
  grep -q "^Digest: *${digest}\$" "${f}" || ok=false
  record "${f}" "docker pull ${MUSICLIB_IMAGE}" docker pull "${MUSICLIB_IMAGE}" || ok=false
  record "${f}" "docker run --rm --network none --entrypoint ls ${MUSICLIB_IMAGE} -l /usr/local/bin" \
    "${run[@]}" --entrypoint ls "${MUSICLIB_IMAGE}" -l /usr/local/bin || ok=false
  local tool
  for tool in ffmpeg ffprobe; do
    record "${f}" "docker run --rm --network none --entrypoint /usr/local/bin/${tool} ${MUSICLIB_IMAGE} -hide_banner -version" \
      "${run[@]}" --entrypoint "/usr/local/bin/${tool}" "${MUSICLIB_IMAGE}" -hide_banner -version || ok=false
    grep -q "^${tool} version 8\.1\.3-musiclib1 " "${f}" || ok=false
  done
  record "${f}" "docker run --rm --network none --entrypoint /usr/local/bin/ffmpeg ${MUSICLIB_IMAGE} -hide_banner -muxers | grep -w hash" \
    bash -c '"$@" | grep -w hash' bash "${run[@]}" --entrypoint /usr/local/bin/ffmpeg "${MUSICLIB_IMAGE}" -hide_banner -muxers || ok=false
  grep -q '^  E  hash  *Hash testing$' "${f}" || ok=false

  local verdict=CONFIRMED
  "${ok}" || verdict=FALSE
  spike_put fragments/H1.md <"${f}"
  printf '%s\tthe image is pulled by its pinned digest; ffmpeg and ffprobe in /usr/local/bin report 8.1.3-musiclib1; the hash muxer is there' \
    "${verdict}" | spike_put "verdicts/H1 image and tools"
}

# H7 (offline): stops the app, runs `musiclibd rebuild` while a watcher
# polls /musiclib/.maintenance, and starts the app again.
check_rebuild() {
  local f="${SPIKE_TMP}/H7c.md" store_id watcher rc=0 i
  spike_tool snapshot || die "the snapshot before the rebuild failed"
  store_id="$(compose_spike run --rm --no-deps -T tools cat /work/store_id)"
  cat >"${f}" <<'EOF'
### The offline rebuild

`docs/operations.md` of MusicLib, "rebuild: regenerate the library folder": the app stopped, `rebuild --store-id` in a one-off container, the app started again. A watcher container (`spike watch-maintenance`, the data volume read-only) polled `/musiclib/.maintenance` during the command. Host commands (`compose` is `docker compose -p vibrance-spike -f scripts/spike/compose.yaml`):

EOF
  record "${f}" "compose stop app" compose_spike stop app || die "stopping the app failed"
  watcher="$(compose_spike run --detach --rm --no-deps tools /work/bin/spike watch-maintenance)"
  for ((i = 0; i < 120; i++)); do
    docker logs "${watcher}" 2>/dev/null | grep -q '^watching ' && break
    sleep 0.5
  done
  ((i < 120)) || die "the maintenance watcher did not start"
  record "${f}" "compose run --rm --no-deps app rebuild --store-id ${store_id}" \
    compose_spike run --rm --no-deps -T app rebuild --store-id "${store_id}" || rc=$?
  docker stop --time 30 "${watcher}" >/dev/null
  spike_put fragments/H7c.md <"${f}"
  ((rc == 0)) || die "the rebuild failed (exit status ${rc}); see the output above"
  spike_tool after-rebuild || die "reading the volume after the rebuild failed"
  info "starting the app again"
  compose_spike up --detach --wait app || die "MusicLib did not become healthy after the rebuild"
  spike_tool rebuild-check || die "the check after the rebuild failed"
}

spike_begin
check_h1
spike_inputs
spike_import
spike_tool analyze || die "H4, H6, H8, H9 failed to run"
spike_tool edits || die "H2, H5 failed to run"
spike_tool tags || die "H3 failed to run"
spike_tool online || die "H7 (online) failed to run"
check_rebuild
spike_tool report
