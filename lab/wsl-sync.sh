#!/bin/sh
# Stage the cross-compiled binaries, rules and lab scripts into a WSL2 distro
# and run the acceptance suite there.
#
# The source tree is reached through a space-free path so that nothing has to be
# quoted through three layers of shell. On Windows, make one with:
#
#   New-Item -ItemType Junction -Path C:\netrewind-src -Target "C:\My project\NetRewind"
#
# Then, from Windows:
#   wsl -d netrewind-lab -u root -- sh /mnt/c/netrewind-src/lab/wsl-sync.sh [all|accept]

set -eu

SRC="${SRC:-/mnt/c/netrewind-src}"
DEST="${DEST:-/opt/netrewind}"
ACTION="${1:-all}"

[ -d "$SRC" ] || { echo "source tree not found at $SRC" >&2; exit 1; }

mkdir -p "$DEST/build"
cp "$SRC/build/netrewindd-linux-amd64" "$DEST/build/netrewindd"
cp "$SRC/build/netrewind-linux-amd64" "$DEST/build/netrewind"
rm -rf "$DEST/rules"
cp -r "$SRC/rules" "$DEST/rules"

for script in inject.sh accept-m0.sh; do
    cp "$SRC/lab/$script" "$DEST/$script"
    # The tree is edited on Windows; strip the line endings it leaves behind.
    sed -i 's/\r$//' "$DEST/$script"
done
chmod +x "$DEST"/*.sh "$DEST"/build/*

cd "$DEST"
case "$ACTION" in
    all)    ./inject.sh all ;;
    accept) ./accept-m0.sh ./build ;;
    stage)  echo "staged into $DEST" ;;
    *)      echo "usage: wsl-sync.sh {all|accept|stage}" >&2; exit 1 ;;
esac
