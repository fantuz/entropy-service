import unittest

from ci_gate import failures


class GateTests(unittest.TestCase):
    required = ("plan", "docs", "quality", "fuzz", "windows-package")
    skips = ("quality", "fuzz")

    def test_full_requires_every_job_to_succeed(self):
        needs = {name: {"result": "success"} for name in self.required}
        self.assertEqual([], failures(needs, "full", self.required, self.skips))
        for status in ("failure", "cancelled", "skipped", "unknown"):
            for name in self.required:
                changed = dict(needs, **{name: {"result": status}})
                self.assertTrue(failures(changed, "full", self.required, self.skips))
        del needs["fuzz"]
        self.assertTrue(failures(needs, "full", self.required, self.skips))

    def test_docs_allows_only_explicit_skips(self):
        needs = {name: {"result": "skipped" if name in self.skips else "success"}
                 for name in self.required}
        self.assertEqual([], failures(needs, "docs", self.required, self.skips))
        needs["windows-package"] = {"result": "skipped"}
        self.assertTrue(failures(needs, "docs", self.required, self.skips))
        self.assertTrue(failures(needs, "", self.required, self.skips))

    def test_failed_plan_cannot_select_docs(self):
        needs = {name: {"result": "success"} for name in self.required}
        needs["plan"] = {"result": "failure"}
        self.assertTrue(failures(needs, "docs", self.required, self.skips))
