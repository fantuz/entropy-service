"""Recovery cannot replace failed tests, stale evidence or a moved release tag."""
import copy
from datetime import datetime, timedelta, timezone
from pathlib import Path
import unittest

import yaml

from validate_release_recovery import GUI_ARTIFACT_PREFIX, GUI_GATE, LEGACY_GUI_STEP, REQUIRED, SPLIT_GUI_JOBS, validate


SHA = "1" * 40
NOW = datetime(2026, 11, 17, tzinfo=timezone.utc)


def passed(name):
    return {"name": name, "status": "completed", "conclusion": "success"}


def evidence(split=False):
    run = {"event": "push", "path": ".github/workflows/release.yml", "head_branch": "v2.4.1",
           "head_sha": SHA, "status": "completed", "conclusion": "failure", "created_at": NOW.isoformat()}
    jobs = [passed(name) for name in sorted(REQUIRED)]
    gate = next(job for job in jobs if job["name"] == GUI_GATE)
    gate["steps"] = [passed(GUI_ARTIFACT_PREFIX + "98765" if split else LEGACY_GUI_STEP)]
    if split:
        jobs.extend(passed(name) for name in sorted(SPLIT_GUI_JOBS))
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
        self.assertEqual("", validate(run, jobs, "v2.4.1", SHA, NOW))

    def test_retried_publication_uses_qualified_artifact_id(self):
        run, jobs = evidence(split=True)
        run["run_attempt"] = 2
        self.assertEqual("98765", validate(run, jobs, "v2.4.1", SHA, NOW))
        path = Path(__file__).resolve().parents[1] / ".github/workflows/release-recovery.yml"
        steps = yaml.load(path.read_text(), Loader=yaml.BaseLoader)["jobs"]["recover"]["steps"]
        by_id, legacy = [s for s in steps if s.get("with", {}).get("path") == "source/bin/gui-release"]
        self.assertEqual("steps.validation.outputs.gui_artifact_id != ''", by_id["if"])
        self.assertEqual("${{ steps.validation.outputs.gui_artifact_id }}", by_id["with"]["artifact-ids"])
        self.assertEqual("true", by_id["with"]["merge-multiple"])
        self.assertEqual("steps.validation.outputs.gui_artifact_id == ''", legacy["if"])
        self.assertEqual("windows-gui-installer-and-sources", legacy["with"]["name"])
        for step in (by_id, legacy):
            self.assertEqual("${{ inputs.validated_run_id }}", step["with"]["run-id"])
            self.assertNotIn("continue-on-error", step)

    def test_recovery_matches_installer_workflow_evidence_names(self):
        workflows = Path(__file__).resolve().parents[1] / ".github/workflows"
        caller = yaml.load((workflows / "release.yml").read_text(), Loader=yaml.BaseLoader)
        prefix = caller["jobs"]["gui-installer"]["name"] + " / "
        installer = yaml.load((workflows / "windows-gui-installer.yml").read_text(), Loader=yaml.BaseLoader)["jobs"]
        qualify = installer["qualify"]
        required = {prefix + qualify["name"].replace("${{ matrix.suite }}", suite)
                    for suite in qualify["strategy"]["matrix"]["suite"]}
        self.assertEqual(SPLIT_GUI_JOBS, required | {prefix + installer["build"]["name"]})
        self.assertEqual(GUI_GATE, prefix + installer["installer"]["name"])
        attest = next(s for s in installer["installer"]["steps"] if s.get("id") == "attest")
        self.assertEqual(GUI_ARTIFACT_PREFIX + "${{ needs.build.outputs.gui_artifact_id }}", attest["name"])

    def test_new_gate_requires_builder_and_both_qualifications(self):
        for name in SPLIT_GUI_JOBS:
            for conclusion in ("missing", "failure", "skipped", "cancelled"):
                with self.subTest(name=name, conclusion=conclusion):
                    run, jobs = evidence(split=True)
                    job = next(j for j in jobs if j["name"] == name)
                    if conclusion == "missing":
                        jobs.remove(job)
                    else:
                        job["conclusion"] = conclusion
                    with self.assertRaises(ValueError):
                        validate(run, jobs, "v2.4.1", SHA, NOW)
        run, jobs = evidence(split=True)
        with self.assertRaises(ValueError):
            validate(run, [j for j in jobs if j["name"] not in SPLIT_GUI_JOBS], "v2.4.1", SHA, NOW)

    def test_recovery_rejects_missing_ambiguous_or_unverified_gui_evidence(self):
        for split in (False, True):
            for change in ("missing", "skipped", "invalid"):
                with self.subTest(split=split, change=change):
                    run, jobs = evidence(split=split)
                    steps = next(j["steps"] for j in jobs if j["name"] == GUI_GATE)
                    if change == "missing":
                        steps.clear()
                    elif change == "skipped":
                        steps[0]["conclusion"] = "skipped"
                    else:
                        steps[0]["name"] += "-invalid"
                    with self.assertRaises(ValueError):
                        validate(run, jobs, "v2.4.1", SHA, NOW)
        run, jobs = evidence(split=True)
        next(j["steps"] for j in jobs if j["name"] == GUI_GATE).append(passed(GUI_ARTIFACT_PREFIX + "9"))
        with self.assertRaises(ValueError):
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
