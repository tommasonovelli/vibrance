#!/usr/bin/env bash
# The probe of scripts/stack-smoke.sh: HTTP requests to Vibrance and MusicLib
# from a container on the stack's network, so that the stack publishes no
# port on the host. It runs in the `probe` service the smoke test adds.
#
# Usage: stack-probe.sh vibrance-ready | musiclib-ready | import |
#                       vibrance-album TITLE | strong-etag | refused HOST
#
# Environment: VIBRANCE_URL and MUSICLIB_URL, the public origins;
# VIBRANCE_CONNECT and MUSICLIB_CONNECT, curl's --connect-to from the host
# and port of the origin to the service; CACERT, a CA file for https
# (optional); VIBRANCE_ADMIN_USERNAME, VIBRANCE_ADMIN_PASSWORD and
# MUSICLIB_PASSWORD. The passwords go to curl on its standard input, never
# on a command line, and nothing here prints them, a cookie or a response
# of a sign-in (DESIGN.md I5).
set -euo pipefail

die() {
  printf 'stack-probe: error: %s\n' "$*" >&2
  exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf -- "${tmp}"' EXIT

# curl to one of the two products: $1 is vibrance or musiclib, the rest are
# curl's arguments. Prints the HTTP status; the body goes to ${tmp}/body.
call() {
  local product="$1" connect
  shift
  case "${product}" in
    vibrance) connect="${VIBRANCE_CONNECT}" ;;
    musiclib) connect="${MUSICLIB_CONNECT}" ;;
  esac
  curl -sS --max-time 20 --connect-to "${connect}" ${CACERT:+--cacert "${CACERT}"} \
    -o "${tmp}/body" -w '%{http_code}' "$@"
}

# Waits up to 90 s for /health/ready of a product to answer 200.
ready() {
  local product="$1" url="$2" status=""
  for _ in $(seq 90); do
    status="$(call "${product}" "${url}/health/ready" 2>/dev/null)" || status="none"
    [[ "${status}" != 200 ]] || { printf '%s: ready\n' "${url}"; return 0; }
    sleep 1
  done
  die "${url}/health/ready answers ${status}, not 200"
}

# Signs in to Vibrance as the first admin, keeping the cookie in ${tmp}.
vibrance_login() {
  local status
  status="$(printf '{"username":"%s","password":"%s"}' "${VIBRANCE_ADMIN_USERNAME}" "${VIBRANCE_ADMIN_PASSWORD}" |
    call vibrance -c "${tmp}/vibrance.jar" -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
      --data-binary @- "${VIBRANCE_URL}/api/v1/auth/login")"
  [[ "${status}" == 200 ]] || die "signing in to Vibrance answers ${status}"
}

# Imports the whole import folder into MusicLib through its API and waits
# for the batch to complete with every album imported.
musiclib_import() {
  local status id
  status="$(printf '%s' "${MUSICLIB_PASSWORD}" |
    call musiclib -c "${tmp}/musiclib.jar" --data-urlencode password@- "${MUSICLIB_URL}/login")"
  [[ "${status}" == 303 ]] || die "signing in to MusicLib answers ${status}, not 303"
  id="$(cat /proc/sys/kernel/random/uuid)"
  status="$(call musiclib -b "${tmp}/musiclib.jar" -H 'X-Musiclib-Request: 1' -H 'Content-Type: application/json' \
    --data-binary "{\"id\":\"${id}\",\"path\":\"\"}" "${MUSICLIB_URL}/api/imports")"
  [[ "${status}" == 201 ]] || die "POST /api/imports answers ${status}: $(cat "${tmp}/body")"
  for _ in $(seq 120); do
    status="$(call musiclib -b "${tmp}/musiclib.jar" "${MUSICLIB_URL}/api/imports/${id}")"
    [[ "${status}" == 200 ]] || die "GET /api/imports/${id} answers ${status}"
    if grep -q '"state":"completed"' "${tmp}/body"; then
      grep -q '"result_album_id":"' "${tmp}/body" || die "the import completed without an album: $(cat "${tmp}/body")"
      printf 'MusicLib imported the import folder\n'
      return 0
    fi
    sleep 1
  done
  die "the import did not complete in 120 s: $(cat "${tmp}/body")"
}

# Waits up to 180 s for Vibrance to list an album with this title, asking
# for a scan every few seconds: MusicLib publishes an album in library/ some
# time after the import.
vibrance_album() {
  local title="$1" status
  vibrance_login
  for _ in $(seq 60); do
    status="$(call vibrance -b "${tmp}/vibrance.jar" "${VIBRANCE_URL}/api/v1/albums")"
    [[ "${status}" == 200 ]] || die "GET /api/v1/albums answers ${status}"
    if grep -qF "\"title\":\"${title}\"" "${tmp}/body"; then
      printf 'Vibrance lists the album %s\n' "${title}"
      return 0
    fi
    status="$(call vibrance -b "${tmp}/vibrance.jar" -X POST -H 'X-Vibrance-Request: 1' \
      "${VIBRANCE_URL}/api/v1/admin/library/scan")"
    [[ "${status}" == 202 ]] || die "POST /api/v1/admin/library/scan answers ${status}"
    sleep 3
  done
  die "Vibrance does not list the album ${title} after 180 s: $(cat "${tmp}/body")"
}

# The ETag of the specification reaches the client strong: no proxy in the
# way compresses or rewrites it (DESIGN.md T24).
strong_etag() {
  local status etag
  status="$(call vibrance -D "${tmp}/headers" "${VIBRANCE_URL}/api/openapi.yaml")"
  [[ "${status}" == 200 ]] || die "GET /api/openapi.yaml answers ${status}"
  etag="$(sed -n 's/^[Ee][Tt][Aa][Gg]: *//p' "${tmp}/headers" | tr -d '\r')"
  [[ "${etag}" =~ ^\"[0-9a-f]{64}\"$ ]] || die "the ETag of /api/openapi.yaml is '${etag}', not a strong SHA-256"
  printf 'strong ETag through %s\n' "${VIBRANCE_URL}"
}

# A request for another name of the domain gets no answer from a product.
refused() {
  local host="$1" status
  if status="$(curl -sS --max-time 20 --connect-to "${host}:443:caddy:443" ${CACERT:+--cacert "${CACERT}"} \
    -o /dev/null -w '%{http_code}' "https://${host}/health/ready" 2>/dev/null)"; then
    die "https://${host} answers ${status}: Caddy must refuse a name it does not route"
  fi
  printf 'https://%s is refused\n' "${host}"
}

case "${1:-}" in
  vibrance-ready) ready vibrance "${VIBRANCE_URL}" ;;
  musiclib-ready) ready musiclib "${MUSICLIB_URL}" ;;
  import) musiclib_import ;;
  vibrance-album) [[ $# -eq 2 ]] || die "usage: stack-probe.sh vibrance-album TITLE"; vibrance_album "$2" ;;
  strong-etag) strong_etag ;;
  refused) [[ $# -eq 2 ]] || die "usage: stack-probe.sh refused HOST"; refused "$2" ;;
  *) die "usage: stack-probe.sh vibrance-ready|musiclib-ready|import|vibrance-album TITLE|strong-etag|refused HOST" ;;
esac
