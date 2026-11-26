#!/usr/bin/env bash
# Inspect each Linux package and portable Unix GUI archive from the same build.
set -euo pipefail

dist=$(realpath "${1:?expected distribution directory}")
prefix="$dist/entropy-service_${2:?expected release version without leading v}"
repo=$(cd "$(dirname "$0")/.." && pwd)
source_dir="$repo/tools/desktop-gui"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

check_file() {
    local source=$1 target=$2 expected=$3 actual
    cmp "$source" "$target"
    actual=$(stat -c %a "$target")
    if [[ "$actual" != "$expected" ]]; then
        echo "$target has mode $actual, expected $expected" >&2
        exit 1
    fi
}

check_gui() {
    local app=$1 module
    check_file "$source_dir/entropy-gui" "$app/entropy-gui" 755
    for module in "$source_dir"/entropy_gui/*.py; do
        check_file "$module" "$app/entropy_gui/${module##*/}" 644
    done
    if [[ -n $(find "$app" \( -name '*.pyc' -o -name __pycache__ \) -print -quit) ]]; then
        echo "Package contains generated Python bytecode: $app" >&2
        exit 1
    fi
}

for platform in linux_amd64 linux_arm64 linux_armv7 darwin_amd64 darwin_arm64; do
    archive="${prefix}_$platform.tar.gz"
    root="$work/$platform"
    echo "Checking ${archive##*/}"
    mkdir "$root"
    tar -xpf "$archive" -C "$root"
    check_gui "$root"
    cmp "$repo/docs/desktop-gui.md" "$root/docs/desktop-gui.md"
    if [[ "$platform" == darwin_* ]]; then
        check_file "$source_dir/entropy-gui-macos" "$root/entropy-gui-macos" 755
        continue
    fi
    if [[ -e "$root/entropy-gui-macos" ]]; then
        echo "Linux archive contains the macOS launcher: $archive" >&2
        exit 1
    fi
    for format in deb rpm; do
        package="${prefix}_$platform.$format"
        root="$work/$platform-$format"
        echo "Checking ${package##*/}"
        mkdir "$root"
        if [[ "$format" == deb ]]; then
            dpkg-deb --extract "$package" "$root"
        else
            bsdtar -xpf "$package" -C "$root"
        fi
        for role in server client; do
            launcher="$root/usr/bin/entropy-$role-gui"
            target=$(realpath -e "$launcher")
            if [[ "$target" != "$root/"* ]]; then
                echo "Launcher points outside the package: $launcher" >&2
                exit 1
            fi
            check_file "$source_dir/packaged-linux-launcher.sh" "$target" 755
        done
        check_gui "$root/usr/share/entropy-service"
    done
done
