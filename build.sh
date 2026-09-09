#!/usr/bin/env bash
# Automated build script for CWidget (Linux).
#
#   ./build.sh                  # regular build
#   WEBKIT41=1 ./build.sh       # if the system has libwebkit2gtk-4.1 (Ubuntu 22.04+)
#   ARCHIVE=1 ./build.sh        # + tar.gz in build/dist
#   VERSION=2.0.0 ./build.sh    # one-off build with a different version
set -euo pipefail
cd "$(dirname "$0")"

command -v go    >/dev/null || { echo "ERROR: go not found" >&2; exit 1; }
command -v wails >/dev/null || { echo "ERROR: wails CLI not found: go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0" >&2; exit 1; }

# --- Version ---
VERSION="${VERSION:-$(head -n1 VERSION | tr -d '[:space:]')}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "ERROR: version '$VERSION' is not in 1.2.3 format" >&2; exit 1; }

echo
echo "== Building CWidget v${VERSION} =="

# --- Icons: assets/ -> build/ ---
echo "[1/2] Syncing icons (assets/ -> build/)..."
go run ./cmd/icons sync

# --- Build ---
echo "[2/2] wails build..."
MODULE="$(go list -m)"
FLAGS=(build -platform linux/amd64 -trimpath -ldflags "-s -w -X ${MODULE}/app.Version=${VERSION}" -clean)
if [[ "${WEBKIT41:-0}" == "1" ]]; then
  FLAGS+=(-tags webkit2_41)
fi
wails "${FLAGS[@]}"

if [[ "${ARCHIVE:-0}" == "1" ]]; then
  mkdir -p build/dist
  tar -czf "build/dist/CWidget_${VERSION}_linux_amd64.tar.gz" -C build/bin CWidget
  echo "      build/dist/CWidget_${VERSION}_linux_amd64.tar.gz"
fi

echo
echo "Done: build/bin/CWidget (v${VERSION})"