#!/bin/bash
set -xe

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
RELAY_PIPE_NAME="container-desktop-test"
# RELAY_PIPE="\\\\.\\pipe\\${RELAY_PIPE_NAME}"
RELAY_PROGRAM="$PROJECT_HOME/bin/container-desktop-wsl-relay"
DISTRIBUTION=$WSL_DISTRO_NAME

./relay-build.sh

# ./bin/container-desktop-wsl-relay.exe \
#   --distribution="$WSL_DISTRO_NAME" \
#   --named-pipe="$RELAY_PIPE" \
#   --unix-socket="$RELAY_SOCKET" \
#   --relay-program-path="$RELAY_PROGRAM"

./bin/container-desktop-wsl-relay.exe \
    "NPIPE-LISTEN:${RELAY_PIPE_NAME}" \
    "WSL:\"${RELAY_PROGRAM} STDIO UNIX-CONNECT:${RELAY_SOCKET}\",distribution=$DISTRIBUTION"
