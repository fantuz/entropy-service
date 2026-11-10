"""Exercise the repository credential guard using disposable Git indexes."""

import subprocess
import tempfile
import unittest
from pathlib import Path

from check_credential_files import credential_files


class CredentialGuardTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "--quiet")

    def git(self, *args):
        subprocess.run(["git", "-C", str(self.root), *args], check=True)

    def track(self, name, content):
        (self.root / name).write_bytes(content)
        self.git("add", "-f", "--", name)

    def test_rejects_identity_extensions_even_without_pem_contents(self):
        names = ["cert.pem", "secret.KEY", "identity.p12", "identity.pfx"]
        for name in names:
            self.track(name, b"fixture")
        self.assertEqual(credential_files(self.root), sorted(names))

    def test_rejects_private_headers_in_other_extensions(self):
        for kind in ("", "RSA ", "EC ", "OPENSSH ", "ENCRYPTED "):
            name = f"secret-{kind.strip() or 'pkcs8'}.txt"
            header = b"-----BEGIN " + kind.encode() + b"PRIVATE KEY-----\r\n"
            self.track(name, b"prefix\n" + header + b"not a real key\n")
        self.assertEqual(len(credential_files(self.root)), 5)

    def test_allows_code_mentions_and_untracked_runtime_keys(self):
        self.track("code.txt", b'header = "-----BEGIN PRIVATE KEY-----"\n')
        (self.root / "runtime.pem").write_bytes(b"not tracked")
        self.assertEqual(credential_files(self.root), [])

    def test_does_not_follow_symlink_outside_checkout(self):
        with tempfile.TemporaryDirectory() as other:
            secret = Path(other) / "outside"
            secret.write_bytes(b"-----BEGIN " + b"PRIVATE KEY-----\n")
            (self.root / "link").symlink_to(secret)
            self.git("add", "--", "link")
            self.assertEqual(credential_files(self.root), [])


if __name__ == "__main__":
    unittest.main()
