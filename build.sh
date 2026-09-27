#!/usr/bin/env bash
#
# Cross-compile y-http-bench for every mainstream OS/arch into dist/.
#
# macOS / Linux counterpart of build.ps1. Both scripts build the same targets
# and produce byte-identical archives, because packaging (permissions and
# timestamps) is done by tools/pack instead of the system tar/zip - see the
# doc comment in tools/pack/main.go for the reasons.
#
# Usage: ./build.sh [-o OutDir]

set -euo pipefail

usage() {
    cat <<'EOF'
Cross-compile y-http-bench for every mainstream OS/arch.

Usage: ./build.sh [-o OutDir]

Options:
  -o OutDir   output directory (default: dist, relative to this script)
  -h          show this help

Artifacts per platform: y-http-bench-<os>-<arch>.zip for Windows (contains
y-http-bench.exe) and y-http-bench-<os>-<arch>.tar.gz elsewhere (contains
y-http-bench with mode 0755), plus the host platform's binary uncompressed,
plus checksums.txt with their SHA256 sums.
EOF
}

out_dir=dist
while getopts ':ho:' opt; do
    case "$opt" in
        h) usage; exit 0 ;;
        o) out_dir=$OPTARG ;;
        '?') usage >&2; exit 2 ;;
    esac
done

cd "$(dirname "$0")"

if [ -z "$out_dir" ]; then
    echo "OutDir must not be empty" >&2
    exit 2
fi
case "$out_dir" in
    /*) ;;
    *) out_dir=$PWD/$out_dir ;;
esac

# Target matrix: 'os/arch' or 'os/arch/goarm' (GOARM only matters for arm).
# Add or remove one entry to change the matrix.
targets='windows/amd64 windows/arm64 windows/386
linux/amd64 linux/arm64 linux/386 linux/arm/7
linux/ppc64le linux/s390x linux/riscv64 linux/loong64
darwin/amd64 darwin/arm64
freebsd/amd64 freebsd/arm64'

host_os=$(go env GOOS)
host_arch=$(go env GOARCH)

mkdir -p "$out_dir"
# Remove files only (no recursion) so nothing outside the output dir is hit.
find "$out_dir" -maxdepth 1 -type f -delete

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT

export CGO_ENABLED=0

# Packing helper, built for the host here - before GOOS/GOARCH are switched
# below - so it runs on this machine.
go build -o "$staging/pack" ./tools/pack

# build_bin reads $goos / $goarch / $goarm from the surrounding loop.
build_bin() {
    if [ -n "$goarm" ]; then
        GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags '-s -w' -o "$1" .
    else
        GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags '-s -w' -o "$1" .
    fi
}

count=0
for target in $targets; do
    goos=${target%%/*}
    rest=${target#*/}
    goarch=${rest%%/*}
    goarm=''
    case "$rest" in
        */*) goarm=${rest#*/} ;;
    esac

    # Short name inside the archive: no os/arch/version.
    bin_name=y-http-bench
    if [ "$goos" = windows ]; then
        bin_name=$bin_name.exe
    fi
    bin_path=$staging/$bin_name

    arch_label=$goarch
    if [ -n "$goarm" ]; then
        arch_label=$arch_label"v$goarm"
    fi

    if [ "$goos" = windows ]; then
        archive_name=y-http-bench-$goos-$arch_label.zip
    else
        archive_name=y-http-bench-$goos-$arch_label.tar.gz
    fi

    build_bin "$bin_path"
    "$staging/pack" -in "$bin_path" -out "$out_dir/$archive_name" -name "$bin_name" -mode 0755

    note=''
    if [ "$goos" = "$host_os" ] && [ "$goarch" = "$host_arch" ]; then
        cp "$bin_path" "$out_dir/$bin_name"
        note="  + $bin_name"
    fi

    size=$(du -h "$out_dir/$archive_name" | cut -f1)
    printf '  build %-20s -> %s  (%s)%s\n' "$target" "$archive_name" "$size" "$note"
    count=$((count + 1))
done

# SHA256 checksums of every artifact, LF line endings so `sha256sum -c` works.
hash_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1   # macOS
    fi
}
find "$out_dir" -maxdepth 1 -type f ! -name checksums.txt | LC_ALL=C sort | while IFS= read -r file; do
    printf '%s  %s\n' "$(hash_of "$file")" "$(basename "$file")"
done > "$out_dir/checksums.txt"

echo
echo "$count archives + checksums.txt -> $out_dir"
