"""Recovery cannot replace failed tests, stale evidence or a moved release tag."""
import copy
from datetime import datetime, timedelta, timezone
from pathlib import Path
import unittest

import yaml

from validate_release_recovery import REQUIRED, validate


SHA = "1" * 40
NOW = datetime(2026, 11, 17, tzinfo=timezone.utc)


def evidence():
    run = {"event": "push", "path": ".github/workflows/release.yml", "head_branch": "v2.4.1",
           "head_sha": SHA, "status": "completed", "conclusion": "failure", "created_at": NOW.isoformat()}
    jobs = [{"name": name, "status": "completed", "conclusion": "success"} for name in sorted(REQUIRED)]
    jobs.append({"name": "Publish only after full tag validation", "status": "completed", "conclusion": "failure"})
    return run, jobs


class RecoveryTests(unittest.TestCase):
    def test_native_artifact_recovery_never_falls_back_to_a_rebuild(self):
        path = Path(__file__).resolve().parents[1] / ".github/workflows/release-recovery.yml"
        steps = yaml.load(path.read_text(), Loader=yaml.BaseLoader)["jobs"]["recover"]["steps"]
        select = next(s for s in steps if s.get("id") == "packaging")
        self.assertEqual("source", select["working-directory"])
        self.assertIn("scripts/native_release.py", select["run"])
        artifact = next(s for s in steps if s.get("with", {}).get("name") == "native-release-payloads")
        self.assertEqual("${{ inputs.validated_run_id }}", artifact["with"]["run-id"])
        self.assertNotIn("continue-on-error", artifact)
        prepare = next(s for s in steps if "../tools/scripts/native_release.py prepare" in s.get("run", ""))
        legacy = next(s for s in steps if "../tools/scripts/prepare_gui_release.py prepare" in s.get("run", ""))
        self.assertEqual("steps.packaging.outputs.method == 'artifact'", artifact["if"])
        self.assertEqual(artifact["if"], prepare["if"])
        self.assertEqual("steps.packaging.outputs.method == 'goreleaser'", legacy["if"])
        self.assertLess(steps.index(select), steps.index(artifact))
        self.assertLess(steps.index(artifact), steps.index(prepare))

    def test_publication_failure_with_all_validation_passed(self):
        run, jobs = evidence()
        validate(run, jobs, "v2.4.1", SHA, NOW)

    def test_only_unused_recovery_wrapper_may_be_skipped(self):
        run, jobs = evidence()
        jobs.append({"name": "recover-publication", "status": "completed", "conclusion": "skipped"})
        validate(run, jobs, "v2.4.1", SHA, NOW)
        jobs[-1]["name"] = "New required validation"
        with self.assertRaises(ValueError):
            validate(run, jobs, "v2.4.1", SHA, NOW)

    def test_failed_missing_cancelled_or_additional_failed_validation_rejected(self):
        for change in ("missing", "failure", "cancelled", "extra", "duplicate"):
            with self.subTest(change=change):
                run, jobs = evidence()
                if change == "missing":
                    jobs.pop(0)
                elif change == "duplicate":
                    jobs.append(copy.deepcopy(jobs[0]))
                elif change == "extra":
                    jobs.append({"name": "New required validation", "status": "completed", "conclusion": "failure"})
                else:
                    jobs[0]["conclusion"] = change
                with self.assertRaises(ValueError):
                    validate(run, jobs, "v2.4.1", SHA, NOW)

    def test_wrong_source_workflow_event_tag_and_stale_run_rejected(self):
        for key, value in [("head_sha", "2" * 40), ("head_branch", "v2.4.0"), ("event", "pull_request"),
                           ("path", ".github/workflows/build.yml"), ("status", "in_progress"),
                           ("created_at", (NOW - timedelta(days=8)).isoformat())]:
            with self.subTest(key=key):
                run, jobs = evidence()
                run[key] = value
                with self.assertRaises(ValueError):
                    validate(run, jobs, "v2.4.1", SHA, NOW)


if __name__ == "__main__":
    unittest.main()
