set RELAY_SOCKET="/var/run/docker.sock"
set RELAY_PIPE=\\.\pipe\container-desktop-test
set RELAY_PROGRAM="/mnt/c/Workspace/is/container-desktop-wsl-relay/bin/container-desktop-wsl-relay"
set WSL_DISTRO_NAME="Ubuntu-24.04"

call relay-build.cmd

.\bin\container-desktop-wsl-relay.exe  --distribution=%WSL_DISTRO_NAME%  --named-pipe=%RELAY_PIPE%  --unix-socket=%RELAY_SOCKET% --relay-program-path=%RELAY_PROGRAM%
