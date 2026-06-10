#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
exec bash "$ROOT/third_party/scripts/build-libdatachannel.sh"