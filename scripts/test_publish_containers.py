import copy
import hashlib
import json
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest
from unittest.mock import patch

import publish_containers as publication
from publish_containers import validate_request, verify_archive, verify_image, verify_security_reports


class PublicationTests(unittest.TestCase):
    repository = "fantuz/entropy-service"
    sha = "a" * 40

    def test_channels_and_stable_latest(self):
        for tag, latest in [("edge", False), ("v2.3.0", True), ("v2.4.0-rc.1", False)]:
            with self.subTest(tag=tag):
                validate_request(self.repository, tag, self.sha, latest)
        for tag, latest in [("main", False), ("v2.3", False), ("v02.3.0", False),
                            ("edge", True), ("v2.4.0-rc.1", True), ("v2.3.0\nlatest", False)]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                validate_request(self.repository, tag, self.sha, latest)

    def test_rejects_nonimmutable_or_foreign_identifiers(self):
        for repository, sha in [("fantuz/entropy-service/other", self.sha),
                                ("ghcr.io/fantuz/service", self.sha),
                                (self.repository, "main"), (self.repository, "a" * 7)]:
            with self.subTest(repository=repository, sha=sha), self.assertRaises(ValueError):
                validate_request(repository, "edge", sha, False)

    def test_image_must_match_tested_source_role_platform_and_version(self):
        image = {"Os": "linux", "Architecture": "arm", "Variant": "v7", "Config": {
            "User": "65532:65532", "Entrypoint": ["/usr/local/bin/entropy-server"],
            "Labels": {"org.opencontainers.image.source": "https://github.com/" + self.repository,
                       "org.opencontainers.image.revision": self.sha,
                       "org.opencontainers.image.version": "2.3.0"}}}
        verify_image(image, "server", "linux-armv7", self.repository, self.sha, "2.3.0")
        mutations = [("root user", lambda d: d["Config"].update(User="0:0")),
                     ("wrong role", lambda d: d["Config"].update(Entrypoint=["/usr/local/bin/entropy-client"])),
                     ("foreign source", lambda d: d["Config"]["Labels"].update({"org.opencontainers.image.source": "https://github.com/other/repository"})),
                     ("stale commit", lambda d: d["Config"]["Labels"].update({"org.opencontainers.image.revision": "b" * 40})),
                     ("wrong version", lambda d: d["Config"]["Labels"].update({"org.opencontainers.image.version": "edge"})),
                     ("wrong architecture", lambda d: d.update(Architecture="amd64")),
                     ("wrong variant", lambda d: d.update(Variant="v6"))]
        for name, mutate in mutations:
            with self.subTest(name=name), self.assertRaises(ValueError):
                changed = copy.deepcopy(image)
                mutate(changed)
                verify_image(changed, "server", "linux-armv7", self.repository, self.sha, "2.3.0")

    def test_corrupted_or_redirected_archive_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / "images.tar.gz"
            archive.write_bytes(b"test archive content")
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            (root / "checksums.txt").write_text(digest + "  images.tar.gz\n")
            self.assertEqual(verify_archive(root), archive)
            archive.write_bytes(b"changed archive")
            with self.assertRaises(ValueError):
                verify_archive(root)
            (root / "checksums.txt").write_text(digest + "  ../different.tar.gz\n")
            with self.assertRaises(ValueError):
                verify_archive(root)

    def test_security_reports_must_match_image_and_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            image_id = "sha256:" + "b" * 64
            for role in ("server", "client"):
                (root / (role + ".spdx.json")).write_text(json.dumps({"SPDXID": "SPDXRef-DOCUMENT", "spdxVersion": "SPDX-2.3", "packages": [{}]}))
                (root / (role + ".vulnerabilities.json")).write_text(json.dumps({"ArtifactType": "container_image", "Metadata": {"ImageID": image_id}, "Results": []}))
            checksum = "".join(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n" for p in sorted(root.glob("*.json")))
            (root / "security-checksums.txt").write_text(checksum)
            self.assertEqual(verify_security_reports(root, "server", image_id), root / "server.spdx.json")
            with self.assertRaisesRegex(ValueError, "exact tested image"):
                verify_security_reports(root, "server", "sha256:" + "c" * 64)
            (root / "server.spdx.json").write_text("changed SBOM")
            with self.assertRaisesRegex(ValueError, "checksum"):
                verify_security_reports(root, "server", image_id)


class RegistryRetryTests(unittest.TestCase):
    command = ["docker", "image", "push", "ghcr.io/fantuz/entropy-client:edge-linux-arm64"]

    @patch.object(publication, "retry_sleep")
    @patch.object(publication.subprocess, "run")
    def test_unknown_blob_retry_preserves_captured_json(self, run, sleep):
        run.side_effect = [subprocess.CompletedProcess(self.command, 1, b"layer already exists\n", b"unknown blob\n"),
                           subprocess.CompletedProcess(self.command, 0, b'{"digest":"tested"}', b"warning\n")]
        self.assertEqual(publication.registry_command(self.command, capture=True), b'{"digest":"tested"}')
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(2)
        self.assertEqual(run.call_args_list[0], run.call_args_list[1])
        self.assertEqual(run.call_args.kwargs["timeout"], 120)

    @patch.object(publication, "retry_sleep")
    @patch.object(publication.subprocess, "run")
    def test_retry_exhaustion_is_failure_not_success(self, run, sleep):
        run.return_value = subprocess.CompletedProcess(self.command, 1, b"", b"unknown blob")
        with self.assertRaises(subprocess.CalledProcessError):
            publication.registry_command(self.command)
        self.assertEqual(run.call_count, 4)
        self.assertEqual([call.args[0] for call in sleep.call_args_list], [2, 4, 8])

    @patch.object(publication, "retry_sleep")
    @patch.object(publication.subprocess, "run")
    def test_permanent_or_unclassified_errors_fail_without_retry(self, run, sleep):
        for output in (b"unauthorized 503", b"denied: requested access", b"invalid reference format",
                       b"manifest invalid", b"digest invalid", b"MANIFEST_INVALID status 503",
                       b"uploaded 500 bytes\nunexpected permanent error", b"unexpected permanent error"):
            with self.subTest(output=output):
                run.reset_mock()
                run.return_value = subprocess.CompletedProcess(self.command, 1, b"", output)
                with self.assertRaises(subprocess.CalledProcessError):
                    publication.registry_command(self.command)
                run.assert_called_once()
                sleep.assert_not_called()

    @patch.object(publication, "retry_sleep")
    @patch.object(publication.subprocess, "run")
    def test_timeout_is_bounded_and_exhaustion_raises(self, run, sleep):
        run.side_effect = subprocess.TimeoutExpired(self.command, 120)
        with self.assertRaises(subprocess.TimeoutExpired):
            publication.registry_command(self.command)
        self.assertEqual(run.call_count, 4)
        self.assertEqual(sleep.call_count, 3)

    def test_transient_registry_statuses(self):
        for output in (b"received unexpected HTTP status: 503 Service Unavailable", b"429 Too Many Requests",
                       b"blob unknown to registry", b"connection reset by peer", b"TLS handshake timeout"):
            with self.subTest(output=output):
                self.assertTrue(publication.transient_registry_error(output))

    @patch.object(publication, "retry_sleep")
    def test_real_subprocess_retries_same_operation_and_returns_clean_output(self, sleep):
        with tempfile.TemporaryDirectory() as directory:
            counter = Path(directory)/"attempts"
            program = ("import pathlib,sys; p=pathlib.Path(sys.argv[1]); "
                       "n=int(p.read_text())+1 if p.exists() else 1; p.write_text(str(n)); "
                       "print('unknown blob' if n==1 else '{\"digest\":\"validated\"}'); "
                       "sys.exit(1 if n==1 else 0)")
            result = publication.registry_command([sys.executable, "-c", program, str(counter)], capture=True)
            self.assertEqual(json.loads(result), {"digest": "validated"})
            self.assertEqual(counter.read_text(), "2")
            sleep.assert_called_once_with(2)


class StagedPublicationTests(unittest.TestCase):
    sha = "a" * 40
    image_id = "sha256:" + "b" * 64

    def publish(self, *, failed_push=0, wrong_config=False, wrong_index=False, swapped_platforms=False):
        events, parts = [], {}
        def docker(*args, capture=False):
            events.append(args)
            if args[:2] == ("image", "inspect"):
                return json.dumps([{"Id": self.image_id}]).encode()
            if args[:2] == ("image", "push"):
                count = sum(item[:2] == ("image", "push") for item in events)
                if count == failed_push:
                    raise subprocess.CalledProcessError(1, args, stderr=b"unknown blob")
                parts[args[2]] = "sha256:" + f"{count:064x}"
            if args[:3] == ("buildx", "imagetools", "inspect"):
                if args[-1] == "{{json .Manifest}}":
                    return json.dumps({"digest": parts[args[3]]}).encode()
                if args[3] == "--raw" and "@" in args[4]:
                    return json.dumps({"config": {"digest": "wrong" if wrong_config else self.image_id}}).encode()
                destination = args[4].rsplit(":", 1)[0]
                manifests = []
                for platform, (arch, variant) in publication.PLATFORMS.items():
                    digest = parts[f"{destination}:v2.5.0-{platform}"]
                    manifests.append({"digest": "sha256:"+"f"*64 if wrong_index else digest,
                                      "platform": {"os": "linux", "architecture": arch, "variant": variant}})
                if swapped_platforms:
                    manifests[0]["platform"], manifests[1]["platform"] = manifests[1]["platform"], manifests[0]["platform"]
                return json.dumps({"manifests": manifests}).encode()
        with tempfile.TemporaryDirectory() as directory, \
             patch.dict(publication.os.environ, {"GITHUB_STEP_SUMMARY": ""}), \
             patch.object(publication.sys, "argv", ["publish", "--repository", "fantuz/entropy-service",
                          "--tag", "v2.5.0", "--source-sha", self.sha, "--artifacts", directory]), \
             patch.object(publication, "verify_archive", return_value=Path(directory)/"images.tar.gz"), \
             patch.object(publication, "verify_image"), \
             patch.object(publication, "verify_security_reports", return_value=Path(directory)/"client.spdx.json"), \
             patch.object(publication, "registry_command"), \
             patch.object(publication, "docker", side_effect=docker):
            try:
                publication.main()
            except (subprocess.CalledProcessError, ValueError) as error:
                return events, error
        return events, None

    def test_all_six_uploads_finish_before_first_channel_write(self):
        events, error = self.publish()
        self.assertIsNone(error)
        creates = [event for event in events if event[:3] == ("buildx", "imagetools", "create")]
        self.assertEqual(len(creates), 2)
        first = events.index(creates[0])
        self.assertEqual(sum(event[:2] == ("image", "push") for event in events[:first]), 6)
        self.assertTrue(all("@sha256:" in part for create in creates for part in create[5:]))

    def test_failed_client_upload_never_promotes_either_channel(self):
        events, error = self.publish(failed_push=5)
        self.assertIsInstance(error, subprocess.CalledProcessError)
        self.assertFalse(any(event[:3] == ("buildx", "imagetools", "create") for event in events))

    def test_wrong_remote_config_prevents_channel_writes(self):
        events, error = self.publish(wrong_config=True)
        self.assertIsInstance(error, ValueError)
        self.assertFalse(any(event[:3] == ("buildx", "imagetools", "create") for event in events))

    def test_index_cannot_substitute_untested_platform_digests(self):
        _, error = self.publish(wrong_index=True)
        self.assertIsInstance(error, ValueError)

    def test_index_cannot_swap_architecture_labels_between_tested_digests(self):
        _, error = self.publish(swapped_platforms=True)
        self.assertIsInstance(error, ValueError)
        self.assertIn("wrong platform", str(error))


if __name__ == "__main__":
    unittest.main()
