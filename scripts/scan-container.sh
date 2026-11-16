#!/usr/bin/env bash
set -euo pipefail

image=$1
role=$2
output=$3
if [[ "$role" != server && "$role" != client ]]; then exit 2; fi
mkdir -p "$output"
image_id=$(docker image inspect "$image" --format '{{.Id}}')
trivy image --scanners vuln --format json --output "$output/$role.vulnerabilities.json" "$image"
trivy convert --format spdx-json --output "$output/$role.spdx.json" "$output/$role.vulnerabilities.json"
python3 scripts/check_container_scan.py "$output/$role.vulnerabilities.json" --image-id "$image_id"
