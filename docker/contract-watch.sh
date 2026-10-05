#!/bin/sh
# The scenario A15 of scripts/contract.sh (DESIGN.md §12.3): stops Vibrance
# in the middle of a scan. It runs in Vibrance's container (docker compose
# exec, the script on standard input) and waits for an ffprobe or ffmpeg
# process, which the scanner starts only while it indexes an album; then it
# sends the signal $1 (KILL or TERM) to the server. The container's restart
# policy starts the server again. It gives up after 60 seconds.
#
# Usage: sh -s -- KILL|TERM <contract-watch.sh
#
# Output: "watching" once it looks, then "signal <SIG> during <process>" or
# "no scan".
set -u

sig="$1"
end=$(($(date +%s) + 60))
loops=0

# The pid of the process named $1, or nothing.
pid_of() {
  for p in /proc/[0-9]*; do
    { read -r comm <"${p}/comm"; } 2>/dev/null || continue
    if [ "${comm}" = "$1" ]; then
      echo "${p#/proc/}"
      return 0
    fi
  done
  return 1
}

echo watching
while :; do
  for p in /proc/[0-9]*; do
    { read -r comm <"${p}/comm"; } 2>/dev/null || continue
    case "${comm}" in
      ffprobe | ffmpeg)
        if server="$(pid_of vibrance)"; then
          echo "signal ${sig} during ${comm}"
          kill -s "${sig}" "${server}"
          exit 0
        fi
        ;;
    esac
  done
  loops=$((loops + 1))
  if [ $((loops % 500)) -eq 0 ] && [ "$(date +%s)" -ge "${end}" ]; then
    echo "no scan"
    exit 0
  fi
done
