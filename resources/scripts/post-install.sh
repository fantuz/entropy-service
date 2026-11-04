#!/bin/sh
set -e

# Register the unit when systemd is running, but never auto-enable, start
# or restart it. Existing enabled/running installations are not modified.
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
fi
