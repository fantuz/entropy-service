#!/usr/bin/env python3
"""Exercise the real server/client images, including TLS and persistent exports."""

import argparse
import http.client
import json
import os
from pathlib import Path
import ssl
import stat
import subprocess
import tempfile
import time
import uuid

from publish_containers import verify_image


class SmokeTest:
    def __init__(self, args, directory):
        self.args, self.directory = args, directory
        self.docker = ["docker"] + (["--host", args.docker_host] if args.docker_host else [])
        self.platform = ["--platform", args.platform]
        self.prefix = "entropy-container-test-" + uuid.uuid4().hex[:10]
        self.network = self.prefix
        self.containers, self.volumes = [], []
        self.sequence = 0

    def command(self, *args, check=True, timeout=90):
        result = subprocess.run(self.docker + list(args), capture_output=True, timeout=timeout)
        if check and result.returncode:
            raise AssertionError(f"Docker {args[0]} failed: {result.stderr.decode(errors='replace')[-3000:]}")
        return result

    def volume(self, suffix):
        name = self.prefix + "-" + suffix
        self.command("volume", "create", name)
        self.volumes.append(name)
        return name

    def create(self, image, *args, options=()):
        name = self.prefix + "-" + str(self.sequence)
        self.sequence += 1
        self.command("create", "--name", name, *self.platform, *options, image, *args)
        self.containers.append(name)
        return name

    def check_image(self, image, role):
        info = json.loads(self.command("image", "inspect", image).stdout)[0]
        assert info["Config"]["User"] == "65532:65532", info["Config"]
        assert info["Config"]["Entrypoint"] == ["/usr/local/bin/entropy-" + role]
        assert info["Config"]["Labels"]["org.opencontainers.image.source"].startswith("https://github.com/")
        if role == "server":
            assert info["Config"]["Healthcheck"]["Test"] == ["CMD", "/usr/local/bin/entropy-health"]
        if self.args.source_sha:
            platform_id = {"linux/amd64": "linux-amd64", "linux/arm64": "linux-arm64", "linux/arm/v7": "linux-armv7"}[self.args.platform]
            verify_image(info, role, platform_id, self.args.repository, self.args.source_sha, self.args.version)
        container = self.create(image, "-describe-json", options=["--read-only"])
        result = self.command("start", "-a", container)
        descriptor = json.loads(result.stdout)
        assert descriptor["role"] == role and descriptor["live"] is False
        self.command("cp", container + ":/usr/share/doc/entropy-service/LICENSE", str(self.directory / (role + "-LICENSE")))
        self.command("cp", container + ":/usr/share/doc/entropy-service/WORDLIST_NOTICES.txt", str(self.directory / (role + "-WORDLIST_NOTICES")))
        ca = self.directory / (role + "-system-roots.crt")
        self.command("cp", container + ":/etc/ssl/certs/ca-certificates.crt", str(ca))
        assert ssl.create_default_context(cafile=str(ca)).cert_store_stats()["x509_ca"] > 0
        for path in ["/data/key.pem", "/data/cert.pem", "/src/go.mod", "/usr/local/go/bin/go", "/bin/sh"]:
            assert self.command("cp", container + ":" + path, str(self.directory / "unexpected"), check=False).returncode, path
        print(f"PASS {role}: non-root image, CA trust, notices, no bundled identity/toolchain/shell", flush=True)

    def start_server(self, tls_volume=None):
        options = ["--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true",
                   "--health-interval=1s", "--health-start-period=0s", "--health-timeout=3s",
                   "--network", self.network, "--network-alias", "entropy-server",
                   "-p", "127.0.0.1::8080"]
        args = []
        if tls_volume:
            options += ["--mount", f"type=volume,src={tls_volume},dst=/data,readonly"]
            args = ["-device=/dev/urandom", "-require-device=true", "-http=:8080", "-https=:8443",
                    "-enable-https=true", "-cert-file=/data/cert.pem", "-key-file=/data/key.pem"]
        container = self.create(self.args.server_image, *args, options=options)
        self.command("start", container)
        published = self.command("port", container, "8080/tcp").stdout.decode().strip()
        host, port = published.rsplit(":", 1)
        deadline = time.monotonic() + 30
        while True:
            try:
                status, _ = self.get(host, int(port), "/ready")
                if status == 200:
                    break
            except (OSError, http.client.HTTPException):
                pass
            if time.monotonic() > deadline:
                logs = self.command("logs", container, check=False)
                raise AssertionError("Container readiness timeout: " + logs.stderr.decode(errors="replace")[-3000:])
            time.sleep(.1)
        for path, expected in [("/", b"Entropy Service"), ("/api-sources.html", b"Source lab"),
                               ("/api-socket.html", b"Socket lab")]:
            status, body = self.get(host, int(port), path)
            assert status == 200 and expected.lower() in body.lower(), (path, status)
        status, _ = self.get(host, int(port), "/lab/sources", host_header="untrusted.invalid")
        assert status == 403, "Container defaults must preserve LAB Host restrictions"
        while self.command("inspect", "--format", "{{.State.Health.Status}}", container).stdout.strip() != b"healthy":
            if time.monotonic() > deadline:
                raise AssertionError("Docker healthcheck did not become healthy")
            time.sleep(.1)
        failed_probe = self.command("exec", container, "/usr/local/bin/entropy-health", "-url=http://127.0.0.1:8080/missing-readiness", check=False)
        assert failed_probe.returncode == 1, "A missing readiness endpoint must fail the probe"
        if tls_volume:
            assert self.command("exec", container, "/usr/local/bin/entropy-health", "-url=https://127.0.0.1:8443/ready", check=False).returncode == 1
            self.command("exec", container, "/usr/local/bin/entropy-health", "-url=https://127.0.0.1:8443/ready", "-ca-file=/data/cert.pem")
        print("PASS server readiness healthcheck, negative status and explicit TLS trust", flush=True)
        return container

    @staticmethod
    def get(host, port, path, host_header=None):
        connection = http.client.HTTPConnection(host, port, timeout=3)
        try:
            connection.request("GET", path, headers={"Host": host_header} if host_header else {})
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    def client(self, mode, secure=False, trust=None, output=None, success=True):
        scheme = ("https" if secure else "http") if mode == "pull" else ("wss" if secure else "ws")
        port = 8443 if secure else 8080
        endpoint = "/v1/data/random" if mode == "pull" else "/stream"
        flag = "-url" if mode == "pull" else "-wsurl"
        options = ["--rm", *self.platform, "--read-only", "--cap-drop=ALL",
                   "--security-opt=no-new-privileges:true", "--network", self.network,
                   "--env", "NO_PROXY=entropy-server,localhost,127.0.0.1"]
        if trust:
            options += ["--mount", f"type=bind,src={trust},dst=/run/entropy-ca.crt,readonly",
                        "--env", "SSL_CERT_FILE=/run/entropy-ca.crt"]
        if output:
            options += ["--mount", f"type=volume,src={output},dst=/data"]
        args = ["-headless", f"-mode={mode}", f"{flag}={scheme}://entropy-server:{port}{endpoint}",
                "-slice=4096", "-refresh=10ms", "-analysis-bytes=256", "-duration=5s",
                "-output-bytes=8192"]
        if output:
            args += [f"-output=/data/{mode}.bin", "-report=-"]
        else:
            args += ["-output=-"]
        result = self.command("run", *options, self.args.client_image, *args, check=False, timeout=45)
        if success:
            assert result.returncode == 0, result.stderr.decode(errors="replace")[-3000:]
            if output:
                records = [json.loads(line) for line in result.stdout.splitlines()]
                summary = records[-1]
                assert summary["type"] == "run_summary" and not summary["failed"], summary
                assert summary["output_written_bytes"] == 8192 and summary["termination"] == "output_limit", summary
            else:
                assert len(result.stdout) == 8192, len(result.stdout)
        else:
            assert result.returncode != 0 and not result.stdout
            assert b"certificate" in result.stderr.lower(), result.stderr.decode(errors="replace")[-2000:]

    def stop(self, container, logs=False):
        self.command("stop", "--time", "15", container, timeout=20)
        state = json.loads(self.command("inspect", "--format", "{{json .State}}", container).stdout)
        assert state["ExitCode"] == 0 and not state["OOMKilled"], state
        output = self.command("logs", container).stdout if logs else None
        self.command("rm", container)
        self.containers.remove(container)
        return output

    def client_lifecycle(self):
        from test_client_terminal import capture_pty, monitor_screens

        options = ["--read-only", "--network", self.network, "--env", "NO_PROXY=entropy-server",
                   "--env", "TERM=xterm-256color", "--tty"]
        terminal = self.create(self.args.client_image, "-mode=pull", "-url=http://entropy-server:8080/v1/data/random",
                               "-slice=4096", "-refresh=25ms", "-display-interval=100ms",
                               "-layout=waterfall", "-color=never", "-duration=350ms", options=options)
        rendered = capture_pty(self.docker + ["start", "--attach", terminal], dict(os.environ), 100, 40)
        assert monitor_screens(rendered), "Container TTY did not render a waterfall status screen"

        running = self.create(self.args.client_image, "-headless", "-mode=push", "-wsurl=ws://entropy-server:8080/stream",
                              "-slice=4096", "-refresh=25ms", "-analysis-bytes=256", "-duration=1h", "-report=-",
                              options=["--read-only", "--network", self.network, "--env", "NO_PROXY=entropy-server"])
        self.command("start", running)
        deadline = time.monotonic() + 15
        while True:
            records = [json.loads(line) for line in self.command("logs", running).stdout.splitlines()]
            if any(r["type"] == "sample" for r in records):
                break
            if time.monotonic() > deadline:
                raise AssertionError("Headless container client did not receive a sample")
            time.sleep(.1)
        summary = json.loads(self.stop(running, logs=True).splitlines()[-1])
        assert summary["type"] == "run_summary" and summary["termination"] == "interrupted" and not summary["failed"], summary
        print("PASS container TTY waterfall and client PID-1 SIGTERM with complete JSONL summary", flush=True)

    def run(self):
        self.check_image(self.args.server_image, "server")
        self.check_image(self.args.client_image, "client")
        self.command("network", "create", self.network)
        server = self.start_server()
        for mode in ["pull", "push"]:
            self.client(mode)
        output = self.volume("output")
        self.client("pull", output=output)
        inspector = self.create(self.args.client_image, "-describe-json", options=["--mount", f"type=volume,src={output},dst=/data,readonly"])
        exported = self.directory / "pull.bin"
        self.command("cp", inspector + ":/data/pull.bin", str(exported))
        assert exported.stat().st_size == 8192
        assert stat.S_IMODE(exported.stat().st_mode) == 0o600
        self.client_lifecycle()
        self.stop(server)
        print("PASS read-only HTTP/WebSocket service, browser assets, exact raw/file exports, private file mode and SIGTERM", flush=True)

        identity = self.volume("identity")
        provision = self.create(self.args.server_image, "generate-cert", "-cert-file=/data/cert.pem",
                                "-key-file=/data/key.pem", "-hosts=entropy-server,127.0.0.1", "-valid-for=1h",
                                options=["--mount", f"type=volume,src={identity},dst=/data"])
        self.command("start", "-a", provision)
        state = json.loads(self.command("inspect", "--format", "{{json .State}}", provision).stdout)
        assert state["ExitCode"] == 0, state
        ca = self.directory / "public-certificate.pem"
        self.command("cp", provision + ":/data/cert.pem", str(ca))
        ca.chmod(0o644)
        server = self.start_server(tls_volume=identity)
        for mode in ["pull", "push"]:
            self.client(mode, secure=True, success=False)
            self.client(mode, secure=True, trust=ca)
        self.stop(server)
        print("PASS non-root certificate provisioning, HTTPS/WSS trust acceptance and untrusted-certificate rejection", flush=True)

    def cleanup(self):
        for container in reversed(self.containers):
            self.command("rm", "-f", container, check=False)
        self.command("network", "rm", self.network, check=False)
        for volume in reversed(self.volumes):
            self.command("volume", "rm", volume, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-image", default="entropy-service/server:linux-amd64")
    parser.add_argument("--client-image", default="entropy-service/client:linux-amd64")
    parser.add_argument("--platform", default="linux/amd64")
    parser.add_argument("--docker-host", default="")
    parser.add_argument("--repository", default="fantuz/entropy-service")
    parser.add_argument("--source-sha", default="", help="also verify publication metadata when supplied")
    parser.add_argument("--version", default="dev")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="entropy-containers-") as directory:
        smoke = SmokeTest(args, Path(directory))
        try:
            smoke.run()
        finally:
            smoke.cleanup()


if __name__ == "__main__":
    main()
