import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from ci_changes import classify


class ChangeClassificationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "-q")
        self.git("config", "user.name", "CI test")
        self.git("config", "user.email", "ci@example.invalid")
        self.write("README.md", "original")
        self.write("docs/guide.md", "original")
        self.write("server.go", "package main")
        self.commit()
        self.base = self.git("rev-parse", "HEAD").strip()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True, stderr=subprocess.DEVNULL)

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self):
        self.git("add", "--all")
        self.git("commit", "-qm", "fixture")

    def mode(self):
        self.commit()
        return classify(self.base, cwd=self.root)

    def test_regular_markdown_add_modify_delete_and_spaces(self):
        self.write("README.md", "updated")
        (self.root / "docs/guide.md").unlink()
        self.write("docs/sub/space and\nnewline.md", "new")
        self.assertEqual("docs", self.mode())

    def test_runtime_file_even_with_markdown(self):
        self.write("docs/guide.md", "updated")
        self.write("server.go", "package changed")
        self.assertEqual("full", self.mode())

    def test_rename_code_to_documentation_does_not_hide_deletion(self):
        self.git("mv", "server.go", "docs/server.md")
        self.assertEqual("full", self.mode())

    def test_unrecognized_markdown_path(self):
        self.write("cmd/web/help.md", "potential embedded asset")
        self.assertEqual("full", self.mode())

    def test_gitignore_change_is_full(self):
        self.write(".gitignore", "bin/")
        self.assertEqual("full", self.mode())

    @unittest.skipIf(os.name == "nt", "symlink fixture needs Unix")
    def test_symlink_is_not_documentation(self):
        (self.root / "docs/guide.md").unlink()
        (self.root / "docs/guide.md").symlink_to("../server.go")
        self.assertEqual("full", self.mode())

    def test_unknown_base_and_empty_diff_are_full(self):
        for base in ("", "HEAD~1", "0" * 40, self.base):
            self.assertEqual("full", classify(base, cwd=self.root))
