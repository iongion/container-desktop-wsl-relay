@echo off

set GOOS=windows
set GOARCH=amd64
go build --ldflags "-s -w -extldflags \"-fno-PIC -static\"" -buildmode pie -tags "osusergo netgo static_build" -o "bin/container-desktop-wsl-relay.exe"
upx -9 "bin/container-desktop-wsl-relay.exe"

@rem set GOOS=linux
@rem set GOARCH=amd64
@rem go build --ldflags "-s -w -extldflags \"-fno-PIC -static\"" -buildmode pie -tags "osusergo netgo static_build" -o "bin/container-desktop-wsl-relay"
