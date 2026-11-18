"""Contracts that prevent required checks or release validations disappearing."""

from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[1]


def workflow(name):
    # BaseLoader preserves GitHub's `on` key and expression strings verbatim.
    return yaml.load((ROOT / f".github/workflows/{name}.yml").read_text(), Loader=yaml.BaseLoader)


class WorkflowContracts(unittest.TestCase):
    def test_pr_checks_always_start(self):
        for name in ("build", "browser-ui"):
            triggers = workflow(name)["on"]
            self.assertIn("pull_request", triggers)
            self.assertFalse(triggers["pull_request"])

    def test_build_gate_waits_for_every_validation_and_rejects_skips(self):
        jobs = workflow("build")["jobs"]
        gate = jobs["build"]
        required = {"plan", "docs", "quality", "fuzz", "race", "terminal", "packages",
                    "containers", "endurance", "windows", "windows-package", "macos"}
        self.assertEqual(required, set(gate["needs"]))
        self.assertEqual("always()", gate["if"])
        command = gate["steps"][-1]["run"]
        self.assertEqual(required, set(command.split("--required ")[1].split(" --docs-skips")[0].split()))
        self.assertEqual({"quality", "fuzz", "race", "terminal", "packages"},
                         set(command.split("--docs-skips ")[1].split()))
        self.assertIn("build", jobs["publish-containers"]["needs"])

    def test_windows_archive_has_no_fuzz_race_terminal_or_quality_dependency(self):
        for name in ("build", "release"):
            jobs = workflow(name)["jobs"]
            self.assertIn("packages", jobs["windows-package"]["needs"])
            forbidden = {"build", "validate", "quality", "fuzz", "race", "terminal", "windows"}
            self.assertFalse(forbidden.intersection(jobs["windows-package"]["needs"]))
            self.assertFalse(forbidden.intersection(jobs["packages"].get("needs", [])))
        package = workflow("package-snapshot")["jobs"]["package"]
        upload = package["steps"][-1]
        self.assertEqual("windows-x64-release-zip", upload["with"]["name"])
        download = workflow("windows-package")["jobs"]["package"]["steps"][2]
        self.assertEqual(upload["with"]["name"], download["with"]["name"])
        self.assertNotIn("run-id", download["with"])
        self.assertNotIn("github-token", download["with"])

    def test_original_check_names_have_documentation_fast_paths(self):
        expected = {"windows": ("windows", "Native Windows x64"),
                    "windows-package": ("package", "Windows release ZIP"),
                    "endurance": ("endurance", "Endurance (${{ matrix.os }})"),
                    "container-build": ("images", "Images (${{ matrix.id }})")}
        for name, (job_id, check) in expected.items():
            document = workflow(name)
            self.assertEqual("true", document["on"]["workflow_call"]["inputs"]["full"]["default"])
            job = document["jobs"][job_id]
            self.assertEqual(check, job["name"])
            self.assertNotIn("if", job)
            self.assertTrue(any(step.get("name") == "Report documentation fast path" for step in job["steps"]))
            for step in job["steps"][2:]:
                self.assertIn("inputs.full", step["if"])
        browser = workflow("browser-ui")["jobs"]["browser"]
        self.assertEqual(["chromium", "firefox"], browser["strategy"]["matrix"]["browser"])
        self.assertEqual("always()", browser["if"])

    def test_releases_preserve_all_final_gates_and_recovery(self):
        jobs = workflow("release")["jobs"]
        self.assertEqual("Validate tagged commit before publishing", jobs["validate"]["name"])
        self.assertEqual({"docs", "quality", "fuzz", "race", "terminal", "packages"}, set(jobs["validate"]["needs"]))
        self.assertEqual({"validate", "browser", "windows", "windows-package", "endurance", "containers", "gui-installer", "macos"},
                         set(jobs["release"]["needs"]))
        for name in ("docs", "quality", "fuzz", "race", "terminal", "packages"):
            self.assertEqual("inputs.validated_run_id == ''", jobs[name]["if"])
            self.assertEqual("${{ github.ref_name }}", jobs[name]["with"]["release_tag"])
        self.assertEqual("./.github/workflows/release-recovery.yml", jobs["recover-publication"]["uses"])

    def test_offline_bundles_gate_publication_and_use_exact_exports(self):
        jobs = workflow("container-build")["jobs"]
        self.assertEqual("images", jobs["bundles"]["needs"])
        builds = [step for step in jobs["images"]["steps"] if step.get("uses", "").startswith("docker/build-push-action@")]
        self.assertEqual(2, len(builds))
        for step in builds:
            self.assertNotIn("load", step["with"])
            self.assertTrue(step["with"]["outputs"].startswith("type=oci,dest="))
        self.assertEqual("container-oci-bundles", jobs["bundles"]["steps"][-1]["with"]["name"])
        release = workflow("release")["jobs"]["release"]["steps"]
        self.assertTrue(any(step.get("with", {}).get("name") == "container-oci-bundles" for step in release))
        self.assertTrue(any("--oci-artifacts bin/oci-release" in step.get("run", "") for step in release))
        publication = workflow("container-publish")["jobs"]["publish"]["steps"]
        self.assertTrue(any(step.get("with", {}).get("pattern") == "container-oci-bundles" for step in publication))

    def test_optional_oci_downloads_have_explicit_consumer_directories(self):
        # download-artifact v8 flattens one pattern match even without
        # merge-multiple. Pin the destination instead of assuming a name folder.
        expected = {
            "container-publish": ["tested-images/container-oci-bundles"],
            "release-recovery": ["source/bin/oci-release-download/container-oci-bundles",
                                 "source/tested-images/container-oci-bundles"],
        }
        for name, paths in expected.items():
            downloads = [step["with"] for job in workflow(name)["jobs"].values()
                         for step in job.get("steps", [])
                         if step.get("with", {}).get("pattern") == "container-oci-bundles"]
            with self.subTest(workflow=name):
                self.assertEqual(paths, [step["path"] for step in downloads])
                self.assertTrue(all(step.get("merge-multiple") == "true" for step in downloads))
                self.assertTrue(all("name" not in step for step in downloads))

    def test_cache_maintenance_uses_only_default_branch_tools(self):
        document = workflow("cache-maintenance")
        self.assertEqual(["closed"], document["on"]["pull_request_target"]["types"])
        checkout = document["jobs"]["cleanup"]["steps"][0]
        self.assertEqual("${{ github.event.repository.default_branch }}", checkout["with"]["ref"])
        self.assertEqual("false", checkout["with"]["persist-credentials"])
        self.assertEqual("true", document["on"]["workflow_dispatch"]["inputs"]["dry_run"]["default"])
        self.assertEqual("write", document["permissions"]["actions"])
        self.assertEqual("read", workflow("build")["permissions"]["contents"])

    def test_go_and_browser_caches_contain_inputs_only(self):
        document = yaml.load((ROOT / ".github/actions/setup-go-cache/action.yml").read_text(), Loader=yaml.BaseLoader)
        steps = document["runs"]["steps"]
        self.assertEqual("false", steps[0]["with"]["cache"])
        caches = [step for step in steps if step.get("uses", "").startswith("actions/cache@")]
        self.assertEqual(2, len(caches))
        self.assertEqual({"${{ steps.paths.outputs.modules }}", "${{ steps.paths.outputs.build }}"},
                         {step["with"]["path"] for step in caches})
        self.assertIn("inputs.profile", caches[1]["with"]["key"])
        self.assertIn("hashFiles('**/*.go'", caches[1]["with"]["key"])
        browser_steps = workflow("browser-ui")["jobs"]["browser"]["steps"]
        browser_cache = next(step for step in browser_steps if step.get("uses", "").startswith("actions/cache@"))
        self.assertIn("matrix.browser", browser_cache["with"]["key"])
        self.assertIn("steps.playwright.outputs.version", browser_cache["with"]["key"])
        install = next(step for step in browser_steps if step.get("name") == "Install OS dependencies and verify matching browser revision")
        self.assertIn("install-deps", install["run"])
        self.assertIn("playwright install ${{ matrix.browser }}", install["run"])
        self.assertNotIn("cache-hit", install["if"])
