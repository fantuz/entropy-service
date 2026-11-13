"""Check that incomplete/failed workload evidence cannot be reported as a pass."""

import json
from pathlib import Path
import tempfile
import unittest

import run_endurance


class EnduranceEvidenceTests(unittest.TestCase):
    def test_requires_every_unique_scenario_with_progress(self):
        good = [{"scenario": name, "failed": False, "cycles": 2, "unexpected_errors": []}
                for name in sorted(run_endurance.SCENARIOS)]
        self.assertTrue(run_endurance.results_complete(good))
        for incomplete in ([], good[:-1], [good[0]] * 3,
                           [dict(result, failed=True) for result in good],
                           [dict(result, cycles=0) for result in good],
                           [dict(result, unexpected_errors=["I/O failed"]) for result in good]):
            self.assertFalse(run_endurance.results_complete(incomplete))

    def test_reads_structured_results_without_treating_logs_as_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "endurance.log"
            record = {"scenario": "source_cycles", "failed": False, "cycles": 3}
            path.write_text("=== RUN TestEndurance\nsource error\n    endurance_test.go:1: "
                            + run_endurance.RESULT_MARKER + json.dumps(record) + "\nPASS\n",
                            encoding="utf-8")
            self.assertEqual(run_endurance.read_results(path), [record])
            self.assertFalse(run_endurance.results_complete(run_endurance.read_results(path)))
            path.write_text(run_endurance.RESULT_MARKER + "{invalid}\n", encoding="utf-8")
            with self.assertRaises(ValueError):
                run_endurance.read_results(path)


if __name__ == "__main__":
    unittest.main()
