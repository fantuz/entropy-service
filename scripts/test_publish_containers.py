import copy
import hashlib
from pathlib import Path
import tempfile
import unittest

from publish_containers import validate_request, verify_archive, verify_image


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


if __name__ == "__main__":
    unittest.main()
