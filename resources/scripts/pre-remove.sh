#!/bin/sh
set -e

systemctl stop entropy-server.service || true
systemctl disable entropy-server.service || true

systemctl daemon-reload
