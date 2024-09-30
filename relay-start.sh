#!/bin/bash
set -e

SCRIPTPATH=$0
if [ ! -e "$SCRIPTPATH" ]; then
  case $SCRIPTPATH in
    (*/*) exit 1;;
    (*) SCRIPTPATH=$(command -v -- "$SCRIPTPATH") || exit;;
  esac
fi
dir=$(
  cd -P -- "$(dirname -- "$SCRIPTPATH")" && pwd -P
) || exit
SCRIPTPATH=$dir/$(basename -- "$SCRIPTPATH") || exit
PROJECT_HOME="$(dirname "$SCRIPTPATH")"

RELAY_SOCKET=$(docker context inspect --format json | jq -e ".[0].Endpoints.docker.Host | sub(\"unix://\"; \"\")" | tr -d '"')
RELAY_PIPE="\\\\.\\pipe\\container-desktop-test"
RELAY_PROGRAM="$PROJECT_HOME/container-desktop-wsl-relay.exe"

./relay-build.sh

echo "Starting relay in $PROJECT_HOME - listen to $RELAY_SOCKET and relay to $RELAY_PIPE"
wsl.exe --exec "$PROJECT_HOME/container-desktop-wsl-relay" \
  --pid-file="/tmp/wsl-relay.pid" \
  --socket "$RELAY_SOCKET" \
  --pipe "$RELAY_PIPE" \
  --relay-program-path "$RELAY_PROGRAM"
