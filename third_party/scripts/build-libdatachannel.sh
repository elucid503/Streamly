#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LIBDC="$ROOT/third_party/libdatachannel"

CMAKE="$(command -v cmake)"

REQUIRED_DEPS=(plog usrsctp libjuice json libsrtp)

deps_present() {

	for dep in "${REQUIRED_DEPS[@]}"; do
		if [[ ! -d "$LIBDC/deps/$dep" ]]; then
			return 1
		fi
	done

	return 0

}

is_git_checkout() {

	[[ -d "$LIBDC/.git" ]] && git -C "$LIBDC" rev-parse --is-inside-work-tree &>/dev/null

}

sync_submodules() {

	if ! is_git_checkout; then
		return 0
	fi

	git -C "$LIBDC" submodule update --init --recursive --depth 1

}

ensure_sources() {

	if [[ -f "$LIBDC/include/rtc/rtc.hpp" ]] && deps_present; then
		return 0
	fi

	echo "fetching libdatachannel v0.24.0 with submodules..." >&2
	rm -rf "$LIBDC"

	git clone --depth 1 --branch v0.24.0 --recurse-submodules --shallow-submodules \
		https://github.com/paullouisageneau/libdatachannel.git "$LIBDC"

}

ensure_sources
sync_submodules

if ! deps_present; then
	echo "libdatachannel dependencies are missing under $LIBDC/deps" >&2
	exit 1
fi

mkdir -p "$LIBDC/build"
"$CMAKE" -S "$LIBDC" -B "$LIBDC/build" \
	-DCMAKE_BUILD_TYPE=Release \
	-DBUILD_SHARED_LIBS=OFF \
	-DNO_EXAMPLES=ON \
	-DNO_TESTS=ON \
	-DNO_WEBSOCKET=ON \
	-DNO_MEDIA=OFF

"$CMAKE" --build "$LIBDC/build" --target datachannel-static -j"$(nproc)"

STATIC_LIB="$LIBDC/build/libdatachannel-static.a"

if [[ ! -f "$STATIC_LIB" ]]; then
	echo "expected static library missing: $STATIC_LIB" >&2
	exit 1
fi

echo "libdatachannel built at $STATIC_LIB"