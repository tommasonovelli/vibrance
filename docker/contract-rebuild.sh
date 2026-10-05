#!/bin/sh
# The scenario A11 of scripts/contract.sh (DESIGN.md §12.3): MusicLib's
# offline rebuild, held while it runs. It runs in a one-off container of
# MusicLib's app service with the app stopped (docker compose run, the script
# on standard input), and starts `musiclibd rebuild` as MusicLib's guide
# says. The rebuild keeps the marker /data/.maintenance for a few tens of
# milliseconds only (docs/spike-report.md, H7): as soon as the marker is
# there, the rebuild is stopped with SIGSTOP, so that Vibrance can be looked
# at during it, and continued with SIGCONT once /tmp/continue exists.
#
# Usage: sh -s <contract-rebuild.sh
#
# Output: "frozen" while it holds the rebuild, then the rebuild's own output
# and "rebuild exit <status>". It exits with the rebuild's status, or 1 if
# the rebuild ended before the marker could be held.
set -u

store_id="$(sed -n 's/^store_id=//p' /data/.musiclib-store)"
[ -n "${store_id}" ] || {
  echo "no store_id in /data/.musiclib-store"
  exit 1
}

/usr/local/bin/musiclibd rebuild --store-id "${store_id}" &
rebuild=$!

while [ ! -e /data/.maintenance ]; do
  if ! kill -0 "${rebuild}" 2>/dev/null; then
    wait "${rebuild}"
    echo "the rebuild ended (status $?) before its marker was seen"
    exit 1
  fi
done
kill -s STOP "${rebuild}"
if [ ! -e /data/.maintenance ]; then
  kill -s CONT "${rebuild}"
  wait "${rebuild}"
  echo "the marker was gone when the rebuild stopped (status $?)"
  exit 1
fi
echo frozen

while [ ! -e /tmp/continue ]; do
  sleep 0.2
done
kill -s CONT "${rebuild}"
wait "${rebuild}"
status=$?
echo "rebuild exit ${status}"
exit "${status}"
