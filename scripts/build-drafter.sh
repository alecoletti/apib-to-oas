#!/usr/bin/env bash
# Build Drafter from the vendored source in third_party/ and install the
# resulting binary into internal/drafter/bin/drafter-<GOOS>-<GOARCH> so it
# gets embedded into apib-to-oas via go:embed.
#
# Usage:
#   scripts/build-drafter.sh                       # builds for current host
#   scripts/build-drafter.sh --clean               # wipe build dir first
#   GOOS=darwin GOARCH=amd64 scripts/build-drafter.sh
#   scripts/build-drafter.sh --linux-amd64-docker  # cross-build linux/amd64 in Docker
#
# Requirements: cmake >= 3.6, a C++14 compiler (Apple clang, gcc 5.3+, clang 4+).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${ROOT}/third_party/drafter-v5.1.0"
DEST_DIR="${ROOT}/internal/drafter/bin"

CLEAN=0
DOCKER_LINUX=""
for arg in "$@"; do
  case "$arg" in
    --clean) CLEAN=1 ;;
    --linux-amd64-docker) DOCKER_LINUX="amd64" ;;
    --linux-arm64-docker) DOCKER_LINUX="arm64" ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

if [[ -n "${DOCKER_LINUX}" ]]; then
  # Cross-build linux/<arch> inside a Debian container with cmake + clang.
  docker run --rm --platform="linux/${DOCKER_LINUX}" \
    -v "${SRC}":/src \
    -v "${DEST_DIR}":/out \
    -e "ARCH=${DOCKER_LINUX}" \
    debian:bookworm-slim bash -c '
      set -euo pipefail
      apt-get update -qq
      apt-get install -y --no-install-recommends cmake clang make ca-certificates >/dev/null
      mkdir -p /build && cd /build
      cmake -DCMAKE_BUILD_TYPE=Release \
            -DBUILD_TESTING=OFF \
            -DCMAKE_C_COMPILER=clang \
            -DCMAKE_CXX_COMPILER=clang++ \
            -DCMAKE_CXX_FLAGS="-include utility -Wno-deprecated-declarations -Wno-error" \
            /src
      cmake --build . --target drafter -j"$(nproc)"
      cp packages/drafter/drafter "/out/drafter-linux-${ARCH}"
      chmod +x "/out/drafter-linux-${ARCH}"
      echo "Installed: /out/drafter-linux-${ARCH}"
    '
  exit 0
fi

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
EXT=""
if [[ "${GOOS}" == "windows" ]]; then EXT=".exe"; fi

BUILD="${SRC}/build-${GOOS}-${GOARCH}"
DEST="${DEST_DIR}/drafter-${GOOS}-${GOARCH}${EXT}"

if [[ "${CLEAN}" == "1" ]]; then rm -rf "${BUILD}"; fi
mkdir -p "${BUILD}"
cd "${BUILD}"

CXX_FLAGS="-include utility -Wno-deprecated-declarations -Wno-error"
EXTRA_CMAKE_ARGS=()

# Native cross-arch build on macOS via -arch.
if [[ "${GOOS}" == "darwin" ]]; then
  case "${GOARCH}" in
    amd64) EXTRA_CMAKE_ARGS+=(-DCMAKE_OSX_ARCHITECTURES=x86_64) ;;
    arm64) EXTRA_CMAKE_ARGS+=(-DCMAKE_OSX_ARCHITECTURES=arm64) ;;
  esac
fi

cmake -DCMAKE_BUILD_TYPE=Release \
      -DBUILD_TESTING=OFF \
      -DCMAKE_CXX_FLAGS="${CXX_FLAGS}" \
      "${EXTRA_CMAKE_ARGS[@]}" \
      "${SRC}"

JOBS="$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)"
cmake --build . --target drafter -j"${JOBS}"

BIN="${BUILD}/packages/drafter/drafter${EXT}"
if [[ ! -x "${BIN}" ]]; then
  echo "drafter binary not produced at ${BIN}" >&2
  exit 1
fi

mkdir -p "${DEST_DIR}"
cp "${BIN}" "${DEST}"
chmod +x "${DEST}"
echo "Installed: ${DEST}"

