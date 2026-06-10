#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LIBDC="$ROOT/third_party/libdatachannel"

CMAKE="$(command -v cmake)"

if [[ ! -f "$LIBDC/include/rtc/rtc.hpp" ]]; then
	rm -rf "$LIBDC"
	git clone --depth 1 --branch v0.24.0 https://github.com/paullouisageneau/libdatachannel.git "$LIBDC"
fi

git -C "$LIBDC" submodule update --init --recursive --depth 1

mkdir -p "$LIBDC/build"
"$CMAKE" -S "$LIBDC" -B "$LIBDC/build" \
	-DCMAKE_BUILD_TYPE=Release \
	-DBUILD_SHARED_LIBS=OFF \
	-DNO_EXAMPLES=ON \
	-DNO_TESTS=ON \
	-DNO_WEBSOCKET=ON \
	-DNO_MEDIA=OFF

"$CMAKE" --build "$LIBDC/build" -j"$(nproc)"
echo "libdatachannel built at $LIBDC/build/libdatachannel.a"
