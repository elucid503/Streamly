#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

MINGW_PACKAGES=(
	mingw-w64-x86_64-pkgconf
	mingw-w64-x86_64-ffmpeg
	mingw-w64-x86_64-x264
	mingw-w64-x86_64-opus
	mingw-w64-x86_64-freetype
	mingw-w64-x86_64-fontconfig
	mingw-w64-x86_64-libass
	mingw-w64-x86_64-gcc
	mingw-w64-x86_64-make
	mingw-w64-x86_64-cmake
	mingw-w64-x86_64-openssl
	git
)

usage() {

	cat <<EOF
Usage: $(basename "$0") [--install-deps] [go command args...]

Sets up the MSYS2 MinGW toolchain on Windows so pkg-config finds libav,
then runs Go with CGO enabled.

Default command:
  go build -o streamly ./cmd/streamly

Examples:
  $(basename "$0")
  $(basename "$0") --install-deps
  $(basename "$0") run ./cmd/streamly
  $(basename "$0") build -o streamly.exe ./cmd/streamly
EOF

}

is_windows_shell() {

	case "$(uname -s 2>/dev/null || true)" in
		MINGW* | MSYS*) return 0 ;;
		*) return 1 ;;
	esac

}

find_msys2_root() {

	local candidate

	if [[ -n "${MSYS2_ROOT:-}" ]]; then
		if [[ -x "${MSYS2_ROOT}/usr/bin/pacman" || -x "${MSYS2_ROOT}/usr/bin/pacman.exe" ]]; then
			echo "$MSYS2_ROOT"
			return 0
		fi
	fi

	for candidate in /c/msys64 /msys64; do
		if [[ -x "${candidate}/usr/bin/pacman" || -x "${candidate}/usr/bin/pacman.exe" ]]; then
			echo "$candidate"
			return 0
		fi
	done

	return 1

}

run_pacman() {

	local msys2_root="$1"
	shift

	if command -v pacman &>/dev/null; then
		pacman "$@"
		return
	fi

	"${msys2_root}/usr/bin/bash.exe" -lc "$(printf '%q ' pacman "$@")"

}

install_msys2_deps() {

	local msys2_root="$1"

	echo "installing MSYS2 build dependencies..." >&2
	run_pacman "$msys2_root" -S --needed --noconfirm "${MINGW_PACKAGES[@]}"

}

setup_msys2_env() {

	local msys2_root="$1"

	export MSYS2_ROOT="$msys2_root"
	export PATH="${msys2_root}/mingw64/bin:${msys2_root}/usr/bin:${PATH}"
	export CGO_ENABLED=1
	export CC=gcc

}

require_libav_pkg_config() {

	if pkg-config --exists libavformat libavcodec libavfilter libavutil libswresample; then
		return 0
	fi

	echo "pkg-config cannot find libav dev libraries." >&2
	echo "On Windows, install MSYS2 packages with --install-deps or run from an MSYS2 MinGW64 shell." >&2
	echo "Ensure ${MSYS2_ROOT:-C:/msys64}/mingw64/bin precedes other pkg-config installs on PATH." >&2
	exit 1

}

ensure_go_on_path() {

	local candidate

	if command -v go &>/dev/null; then
		return 0
	fi

	for candidate in \
		"/c/Program Files/Go/bin" \
		"/c/Program Files (x86)/Go/bin"; do
		if [[ -x "${candidate}/go.exe" ]]; then
			export PATH="${candidate}:${PATH}"
			return 0
		fi
	done

	echo "go not found on PATH. Install Go or add it to PATH." >&2
	exit 1

}

INSTALL_DEPS=false
GO_ARGS=()

while [[ $# -gt 0 ]]; do
	case "$1" in
		--install-deps)
			INSTALL_DEPS=true
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		--)
			shift
			GO_ARGS=("$@")
			break
			;;
		*)
			GO_ARGS=("$@")
			break
			;;
	esac
done

if [[ ${#GO_ARGS[@]} -eq 0 ]]; then
	GO_ARGS=(build -o streamly ./cmd/streamly)
fi

cd "$ROOT"

if is_windows_shell; then
	MSYS2_ROOT="$(find_msys2_root || true)"

	if [[ -z "$MSYS2_ROOT" ]]; then
		echo "MSYS2 not found. Install it to C:\\msys64 or set MSYS2_ROOT." >&2
		exit 1
	fi

	if $INSTALL_DEPS; then
		install_msys2_deps "$MSYS2_ROOT"
	fi

	setup_msys2_env "$MSYS2_ROOT"
else
	export CGO_ENABLED=1

	if $INSTALL_DEPS; then
		echo "--install-deps is only supported on Windows with MSYS2." >&2
		exit 1
	fi
fi

require_libav_pkg_config
ensure_go_on_path

exec go "${GO_ARGS[@]}"