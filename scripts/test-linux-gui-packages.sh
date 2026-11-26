#!/usr/bin/env bash
# Inspect the two native package formats without installing anything on the host.
set -euo pipefail

deb=$(realpath "${1:?expected DEB and RPM paths}")
rpm=$(realpath "${2:?expected RPM path}")
source_dir=$(cd "$(dirname "$0")/../tools/desktop-gui" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/deb" "$work/rpm"
dpkg-deb --extract "$deb" "$work/deb"
rpm2cpio "$rpm" | (cd "$work/rpm" && cpio --quiet -idm --no-absolute-filenames)

for root in "$work/deb" "$work/rpm"; do
    for role in server client; do
        launcher="$root/usr/bin/entropy-$role-gui"
        target=$(realpath -e "$launcher")
        [[ "$target" == "$root/"* && $(stat -c %a "$target") == 755 ]] || {
            echo "Unexpected target or permissions for $launcher" >&2
            exit 1
        }
        cmp "$source_dir/packaged-linux-launcher.sh" "$target"
    done
    app="$root/usr/share/entropy-service"
    cmp "$source_dir/entropy-gui" "$app/entropy-gui"
    for module in "$source_dir"/entropy_gui/*.py; do
        installed="$app/entropy_gui/$(basename "$module")"
        cmp "$module" "$installed"
        [[ $(stat -c %a "$installed") == 644 ]]
    done
    if [[ -n $(find "$app" \( -name '*.pyc' -o -name __pycache__ \) -print -quit) ]]; then
        echo "Package contains generated Python bytecode: $root" >&2
        exit 1
    fi
done
