#!/bin/sh
set -eu

# Static packaging smoke checks: no root privileges, real service starts or
# device changes. CI additionally builds the package through GoReleaser.
unit=resources/systemd/entropy-server.service
post=resources/scripts/post-install.sh
pre=resources/scripts/pre-remove.sh

for script in resources/scripts/*-install.sh resources/scripts/*-remove.sh; do
    sh -n "$script"
done

grep -qx 'DynamicUser=yes' "$unit"
grep -qx 'User=entropy-service' "$unit"
grep -qx 'NoNewPrivileges=yes' "$unit"
grep -qx 'DevicePolicy=closed' "$unit"
grep -qx 'DeviceAllow=/dev/urandom r' "$unit"
grep -q 'ExecStart=.*-http=127.0.0.1:8080.*-enable-https=false.*-device=/dev/urandom.*-require-device=true' "$unit"
grep -qx 'LimitCORE=0' "$unit"

# Installation never activates a daemon or creates/chmods host device nodes.
if grep -E 'systemctl[[:space:]]+(enable|start|restart)|(/dev/.*chmod|chmod.* /dev/)|useradd|groupadd' "$post" resources/scripts/pre-install.sh; then
    echo 'install scripts unexpectedly activate service or modify identities/devices' >&2
    exit 1
fi

grep -q 'remove|0)' "$pre"
if command -v systemd-analyze >/dev/null 2>&1 && [ -x /usr/bin/entropy-server ]; then
    systemd-analyze verify "$unit"
fi
printf '%s\n' 'Packaging/service smoke checks passed'
