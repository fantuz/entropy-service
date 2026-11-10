#!/usr/bin/env bash
# Destructive package lifecycle test: disposable systemd VM only, never a user host.
set -euo pipefail

if [[ ${ENTROPY_SYSTEMD_TEST:-} != 1 || $EUID != 0 || $(cat /proc/1/comm) != systemd ]]; then
  echo 'Requires root, systemd PID 1 and explicit ENTROPY_SYSTEMD_TEST=1 on a disposable VM.' >&2
  exit 2
fi
if [[ $# != 1 || ! -f $1 ]]; then
  echo 'Usage: test-systemd-service.sh PACKAGE.deb' >&2
  exit 2
fi
if systemctl cat entropy-server.service >/dev/null 2>&1; then
  echo 'Refusing to replace an existing entropy-server unit.' >&2
  exit 2
fi
for path in /etc/systemd/system/entropy-server.service.d /var/lib/entropy-service /var/lib/private/entropy-service; do
  if [[ -e $path || -L $path ]]; then
    echo "Refusing to replace existing test state: $path" >&2
    exit 2
  fi
done

package=$1
work=$(mktemp -d /tmp/entropy-systemd.XXXXXX)
override=/etc/systemd/system/entropy-server.service.d/override.conf
state=/var/lib/entropy-service
installed=0

cleanup() {
  status=$?
  trap - EXIT
  if (( status != 0 )); then
    systemctl status entropy-server --no-pager -l || true
    journalctl -u entropy-server -b -n 100 --no-pager || true
  fi
  if (( installed )); then
    systemctl stop entropy-server || true
    systemctl clean --what=state entropy-server || true
    rm -rf /etc/systemd/system/entropy-server.service.d
    dpkg --purge entropy-service || true
    systemctl daemon-reload
  fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

fail() { echo "$*" >&2; exit 1; }
wait_ready() {
  for _ in {1..100}; do
    if curl -fsS --max-time 1 "$@" >/dev/null 2>"$work/last-curl-error"; then return; fi
    sleep 0.1
  done
  cat "$work/last-curl-error" >&2
  ss -ltnp '( sport = :8080 or sport = :8443 )' >&2
  stat -Lc '%A %U:%G %n' "$state" "$state/current-cert.pem" "$state/current-key.pem" >&2 || true
  ls -ld /var/lib/entropy-service /var/lib/private/entropy-service >&2 || true
  fail 'Service did not become ready.'
}
write_override() {
  name=$1
  cat > "$override" <<EOF
[Service]
StateDirectory=entropy-service
StateDirectoryMode=0700
ExecStartPre=/usr/bin/entropy-server generate-cert -if-missing -cert-file=$state/$name-cert.pem -key-file=$state/$name-key.pem -hosts=localhost,127.0.0.1,::1
ExecStart=
ExecStart=/usr/bin/entropy-server -http=127.0.0.1:8080 -https=127.0.0.1:8443 -enable-https=true -device=/dev/urandom -require-device=true -cert-file=$state/$name-cert.pem -key-file=$state/$name-key.pem
EOF
  systemctl daemon-reload
}
identity() { openssl x509 -in "$1" -noout -fingerprint -sha256; }
assert_tls_metrics() {
  curl -fsS --max-time 3 http://127.0.0.1:8080/metrics -o "$work/metrics"
  grep -Fxq "rng_https_enabled $1" "$work/metrics" || fail 'HTTPS metric differs from active configuration.'
  if [[ $1 == 1 ]]; then
    expires=$(openssl x509 -in "$2" -noout -enddate)
    timestamp=$(date -u -d "${expires#notAfter=}" +%s)
    grep -Fxq "rng_tls_certificate_not_after_timestamp_seconds $timestamp" "$work/metrics" || fail 'TLS metric differs from the active identity.'
  elif grep -q '^rng_tls_certificate_not_after_timestamp_seconds ' "$work/metrics"; then
    fail 'HTTP-only service emitted a certificate expiry.'
  fi
}

installed=1
dpkg --install -- "$package"
[[ $(systemctl is-enabled entropy-server 2>/dev/null || true) == disabled ]] || fail 'Package unexpectedly enabled the service.'
if systemctl is-active --quiet entropy-server; then fail 'Package unexpectedly started the service.'; fi
systemd-analyze verify /usr/lib/systemd/system/entropy-server.service
systemctl start entropy-server
wait_ready http://127.0.0.1:8080/ready
assert_tls_metrics 0
pid=$(systemctl show entropy-server -p MainPID --value)
[[ $(awk '/^Uid:/{print $2}' "/proc/$pid/status") != 0 ]] || fail 'Service is running as root.'
[[ $(systemctl show entropy-server -p DynamicUser --value) == yes ]] || fail 'DynamicUser sandbox is missing.'
curl -fsS --max-time 3 'http://127.0.0.1:8080/v1/data/random?bytes=64' -o "$work/sample"
[[ $(wc -c < "$work/sample") == 64 ]] || fail 'HTTP payload length differs.'
[[ -z $(ss -H -ltn '( sport = :8443 )') ]] || fail 'Default package unexpectedly listens on HTTPS.'
[[ ! -e $state ]] || fail 'HTTP-only startup unexpectedly provisioned certificate state.'

# Reinstallation/upgrade must preserve the running process and enablement policy.
dpkg --install -- "$package"
[[ $(systemctl show entropy-server -p MainPID --value) == "$pid" ]] || fail 'Package upgrade restarted the service.'
[[ $(systemctl is-enabled entropy-server 2>/dev/null || true) == disabled ]] || fail 'Package upgrade changed enablement.'

mkdir -p "$(dirname "$override")"
write_override current
systemctl restart entropy-server
wait_ready --cacert "$state/current-cert.pem" https://127.0.0.1:8443/ready
wait_ready http://127.0.0.1:8080/ready
[[ $(stat -c %a "$state/current-key.pem") == 600 ]] || fail 'Private key permissions are unsafe.'
[[ $(stat -Lc %a "$state") == 700 ]] || fail 'State directory permissions are unsafe.'
assert_tls_metrics 1 "$state/current-cert.pem"
first=$(identity "$state/current-cert.pem")
systemctl restart entropy-server
wait_ready --cacert "$state/current-cert.pem" https://127.0.0.1:8443/ready
[[ $(identity "$state/current-cert.pem") == "$first" ]] || fail 'Restart changed the TLS identity.'

# Renewal uses new filenames. Keep the previous files/override for rollback.
cp "$override" "$work/previous-override.conf"
cp "$state/current-cert.pem" "$work/previous-public-cert.pem"
systemd-run --quiet --wait --collect --unit="entropy-cert-renewal-$$" \
  -p DynamicUser=yes -p User=entropy-service -p StateDirectory=entropy-service -p StateDirectoryMode=0700 -p UMask=0077 \
  /usr/bin/entropy-server generate-cert -cert-file="$state/renewed-cert.pem" -key-file="$state/renewed-key.pem" -hosts=localhost,127.0.0.1,::1
write_override renewed
systemctl restart entropy-server
wait_ready --cacert "$state/renewed-cert.pem" https://127.0.0.1:8443/ready
assert_tls_metrics 1 "$state/renewed-cert.pem"
[[ $(identity "$state/renewed-cert.pem") != "$first" ]] || fail 'Renewal reused the previous identity.'
if curl -fsS --max-time 2 --cacert "$work/previous-public-cert.pem" https://127.0.0.1:8443/live >/dev/null 2>&1; then
  fail 'Old certificate trust unexpectedly accepts the replacement identity.'
fi

# Inject an expired pair under the real service identity/StateDirectory mapping.
# No private key material is printed or made readable outside that sandbox.
fixture_script=$(cat <<'SH'
set -eu
state=/var/lib/entropy-service
fixture=$(mktemp -d "$state/expiry-fixture.XXXXXX")
trap 'rm -rf "$fixture"' EXIT
mkdir "$fixture/db"
touch "$fixture/index.txt"
printf '01\n' > "$fixture/serial"
cat > "$fixture/ca.cnf" <<EOF
[ca]
default_ca=local
[local]
database=$fixture/index.txt
serial=$fixture/serial
new_certs_dir=$fixture/db
default_md=sha256
policy=subject
[subject]
commonName=supplied
EOF
openssl req -new -key "$state/renewed-key.pem" -subj /CN=localhost -out "$fixture/request.csr"
openssl ca -batch -selfsign -config "$fixture/ca.cnf" -cert "$state/renewed-cert.pem" -keyfile "$state/renewed-key.pem" -in "$fixture/request.csr" -startdate 20000101000000Z -enddate 20000102000000Z -out "$state/expired-cert.pem"
cp "$state/renewed-key.pem" "$state/expired-key.pem"
SH
)
systemd-run --quiet --wait --collect --unit="entropy-cert-fixture-$$" \
  -p DynamicUser=yes -p User=entropy-service -p StateDirectory=entropy-service -p StateDirectoryMode=0700 -p UMask=0077 \
  /bin/sh -c "$fixture_script"
expired=$(identity "$state/expired-cert.pem")
write_override expired
if systemctl restart entropy-server; then fail 'Expired certificate was accepted at startup.'; fi
# Reset while the failed/restarting unit is still loaded, then cancel its restart.
# After stop + daemon-reload, systemd may have already garbage-collected the unit.
systemctl reset-failed entropy-server
systemctl stop entropy-server
[[ -z $(ss -H -ltn '( sport = :8080 or sport = :8443 )') ]] || fail 'TLS provisioning failure left a listener active.'
[[ $(identity "$state/expired-cert.pem") == "$expired" ]] || fail 'Expired identity was silently replaced.'
journalctl -u entropy-server -b --no-pager > "$work/journal.log"
grep -q 'not currently valid' "$work/journal.log" || fail 'Expected expiry diagnostic is missing.'

# Roll back to the retained, still-valid pair and prove trust works again.
cp "$work/previous-override.conf" "$override"
systemctl daemon-reload
systemctl start entropy-server
wait_ready --cacert "$work/previous-public-cert.pem" https://127.0.0.1:8443/ready
[[ $(identity "$state/current-cert.pem") == "$first" ]] || fail 'Rollback changed the retained identity.'
echo 'Real systemd HTTP/TLS, private state, restart, renewal, expiry refusal and rollback checks passed.'
