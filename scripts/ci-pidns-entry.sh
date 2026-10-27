#!/bin/bash
# Ephemeral Linux PID-namespace init. This shell reaps adopted descendants;
# development-tool ownership and sole Wait remain inside the Python driver.
set -u
if [ "$$" -ne 1 ]; then
  echo 'private PID namespace init required' >&2
  exit 1
fi
trap ':' TERM INT
"$@" &
validation_child=$!
wait "$validation_child"
validation_status=$?
exit "$validation_status"
