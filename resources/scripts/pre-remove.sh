#!/bin/sh
set -e

# On removal, stop and disable the installed service. Debian passes "remove",
# RPM passes "0". Do not stop or disable during an upgrade (Debian "upgrade",
# RPM "1"); preserve the operator's service policy.
case "${1:-}" in
    remove|0)
        if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
            systemctl stop entropy-server.service || true
            systemctl disable entropy-server.service || true
        fi
        ;;
esac
