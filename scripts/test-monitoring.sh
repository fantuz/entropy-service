#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
if [[ -n ${PROMTOOL:-} ]]; then
  tool=$PROMTOOL
elif command -v promtool >/dev/null; then
  tool=$(command -v promtool)
else
  # Pinned Prometheus LTS tooling; only the checked promtool binary is extracted.
  version=3.5.5
  archive=prometheus-$version.linux-amd64.tar.gz
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  base=https://github.com/prometheus/prometheus/releases/download/v$version
  curl -fsSL --retry 3 "$base/$archive" -o "$work/$archive"
  curl -fsSL --retry 3 "$base/sha256sums.txt" -o "$work/sha256sums.txt"
  (cd "$work" && sha256sum --check --ignore-missing sha256sums.txt)
  tar -xzf "$work/$archive" -C "$work" "prometheus-$version.linux-amd64/promtool"
  tool=$work/prometheus-$version.linux-amd64/promtool
fi
"$tool" check rules "$root/resources/monitoring/entropy-alerts.yml"
"$tool" test rules "$root/resources/monitoring/entropy-alerts.test.yml"
"$tool" check config --syntax-only "$root/resources/monitoring/prometheus.yml"
