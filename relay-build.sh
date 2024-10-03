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

WINSOCAT_REPO="git@github.com:iongion/WinSocat.git"
WINSOCAT_BRANCH="upgrade-all-deps-update-to-dotnet8"

SOCAT_VERSION="1.8.0.1"
SOCAT_TARBALL="socat-${SOCAT_VERSION}.tar.gz"
SOCAT_PACKAGE="http://www.dest-unreach.org/socat/download/${SOCAT_TARBALL}"

mkdir -p "$PROJECT_HOME/bin"
mkdir -p "$PROJECT_HOME/vendors"

if [[ ! -f "$PROJECT_HOME/vendors/${SOCAT_TARBALL}" ]]; then
  echo "Downloading socat ${SOCAT_VERSION} from $SOCAT_PACKAGE"
  curl -L "$SOCAT_PACKAGE" -o "$PROJECT_HOME/vendors/${SOCAT_TARBALL}"
  tar -xzf "$PROJECT_HOME/vendors/${SOCAT_TARBALL}" -C "$PROJECT_HOME/vendors"
fi

# GOOS=windows GOARCH=amd64 go build --ldflags '-s -w -extldflags "-fno-PIC -static"' -buildmode pie -tags "osusergo netgo static_build" -o "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe"
# chmod +x "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe"
# upx -9 "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe"

# GOOS=linux GOARCH=amd64 go build --ldflags '-s -w -extldflags "-fno-PIC -static"' -buildmode pie -tags "osusergo netgo static_build" -o "$PROJECT_HOME/bin/container-desktop-wsl-relay"
# chmod +x "$PROJECT_HOME/bin/container-desktop-wsl-relay"
# upx -9 "$PROJECT_HOME/bin/container-desktop-wsl-relay"

if [[ ! -d "$PROJECT_HOME/vendors/WinSocat" ]]; then
  git clone --branch "$WINSOCAT_BRANCH" "$WINSOCAT_REPO" "$PROJECT_HOME/vendors/WinSocat"
fi

if [[ -f "$PROJECT_HOME/vendors/WinSocat/winsocat/bin/Release/net8.0/win-x64/publish/winsocat.exe" ]]; then
  echo "winsocat.exe already exists in $PROJECT_HOME/vendors/WinSocat/winsocat/bin/Release/net8.0/win-x64/publish"
else
  cd "$PROJECT_HOME/vendors/WinSocat"
  git fetch origin
  git checkout "$WINSOCAT_BRANCH"
  git pull origin "$WINSOCAT_BRANCH"
  dotnet restore
  cd "winsocat"
  dotnet publish -c Release -r win-x64 -p:PublishSingleFile=true -p:PublishReadyToRun=false --self-contained true -p:PublishTrimmed=true
fi

cp "$PROJECT_HOME/vendors/WinSocat/winsocat/bin/Release/net8.0/win-x64/publish/winsocat.exe" "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe"

if [[ -f "$PROJECT_HOME/vendors/socat-${SOCAT_VERSION}/socat" ]]; then
  echo "socat already exists in $PROJECT_HOME/vendors/socat-${SOCAT_VERSION}/socat"
else
  cd "$PROJECT_HOME/vendors/socat-${SOCAT_VERSION}"
  # Additional flags (if needed)
  export CC="musl-gcc"
  export LD="musl-ld"
  export CFLAGS="-O2 -Wall"
  export LDFLAGS="-static"
  export TARGET=x86_64-linux-musl
  # Static build
  ./configure \
    --prefix="$PROJECT_HOME" \
    --enable-msglevel=DEBUG \
    --disable-largefile \
    --disable-stats \
    --disable-fdnum \
    --disable-file \
    --disable-creat \
    --disable-socketpair \
    --disable-termios \
    --disable-ip4 \
    --disable-ip6 \
    --disable-rawip \
    --disable-interface \
    --disable-tcp \
    --disable-udp \
    --disable-udplite \
    --disable-sctp \
    --disable-dccp \
    --disable-vsock \
    --disable-namespaces \
    --disable-posixmq \
    --disable-socks4 \
    --disable-socks4a \
    --disable-socks5 \
    --disable-openssl \
    --disable-exec \
    --disable-system \
    --disable-shell \
    --disable-pty \
    --disable-fs \
    --disable-readline \
    --disable-tun \
    --disable-sycls \
    --disable-filan \
    --disable-libwrap \
    && echo "Configured socat with minimal features"
  echo "Building socat"
  make clean
  make socat
  strip -S socat
fi

cp "$PROJECT_HOME/vendors/socat-${SOCAT_VERSION}/socat" "$PROJECT_HOME/bin/container-desktop-wsl-relay"

sha256sum "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe" > "$PROJECT_HOME/bin/container-desktop-wsl-relay.exe.sha256"
sha256sum "$PROJECT_HOME/bin/container-desktop-wsl-relay" > "$PROJECT_HOME/bin/container-desktop-wsl-relay.sha256"
