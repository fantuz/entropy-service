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
        self.assertEqual("${{ !cancelled() }}", gate["if"])
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
        self.assertEqual("${{ !cancelled() }}", browser["if"])

    def test_superseded_runs_release_their_qualification_jobs(self):
        for name in ("build", "browser-ui"):
            concurrency = workflow(name)["concurrency"]
            self.assertEqual("true", concurrency["cancel-in-progress"])
            self.assertIn("github.event.pull_request.number || github.ref", concurrency["group"])

        # On a live docs run the package build is deliberately skipped. On a
        # full run only verified packages may reach native platform tests.
        jobs = workflow("build")["jobs"]
        for name in ("macos", "windows-package"):
            self.assertEqual(
                "${{ !cancelled() && needs.plan.result == 'success' && "
                "(needs.plan.outputs.mode == 'docs' || needs.packages.result == 'success') }}",
                jobs[name]["if"])

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

    def test_native_packages_are_built_once_tested_and_promoted(self):
        steps = workflow("package-snapshot")["jobs"]["package"]["steps"]
        builds = [s["with"]["args"] for s in steps if s.get("uses", "").startswith("goreleaser/")]
        self.assertEqual("release --skip=publish --clean", builds[0])
        self.assertIn("--skip=publish", builds[1])
        self.assertIn("native-dry-run.yml", builds[1])
        staged = {s.get("with", {}).get("name"): s["with"]["path"] for s in steps if s.get("uses", "").startswith("actions/upload-artifact@")}
        self.assertEqual(staged["native-release-payloads"], "bin/native-release/")
        for name in ("macos-cli-archives", "windows-x64-release-zip"):
            self.assertTrue(all(line.startswith("bin/native-release/") for line in staged[name].splitlines()))
            self.assertIn("bin/native-release/checksums.txt", staged[name])
        windows = workflow("windows-package")["jobs"]["package"]["steps"]
        self.assertTrue(any("--checksums windows-release/checksums.txt" in s.get("run", "") for s in windows))
        release = workflow("release")["jobs"]["release"]["steps"]
        self.assertTrue(any(s.get("with", {}).get("name") == "native-release-payloads" for s in release))
        self.assertTrue(any("scripts/native_release.py prepare --native bin/native-release" in s.get("run", "") for s in release))

    def test_windows_installer_qualifications_share_one_verified_executable(self):
        jobs = workflow("windows-gui-installer")["jobs"]
        build = jobs["build"]
        checkpoint = next(step for step in build["steps"] if step.get("id") == "checkpoint")
        self.assertEqual("gui-installer/*_gui_setup.exe", checkpoint["with"]["path"])
        self.assertEqual("0", checkpoint["with"]["compression-level"])
        self.assertEqual("${{ steps.checkpoint.outputs.artifact-id }}", build["outputs"]["setup_artifact_id"])

        qualify = jobs["qualify"]
        self.assertEqual("build", qualify["needs"])
        self.assertEqual({"per-user", "service"}, set(qualify["strategy"]["matrix"]["suite"]))
        self.assertEqual("false", qualify["strategy"]["fail-fast"])
        downloads = [step["with"] for step in qualify["steps"]
                     if step.get("uses", "").startswith("actions/download-artifact@")]
        self.assertEqual([{"artifact-ids": "${{ needs.build.outputs.setup_artifact_id }}",
                           "path": "gui-setup-checkpoint", "merge-multiple": "true"}], downloads)
        verify = next(step for step in qualify["steps"] if "EXPECTED_SHA256" in step.get("env", {}))
        self.assertEqual("${{ needs.build.outputs.setup_sha256 }}", verify["env"]["EXPECTED_SHA256"])
        self.assertLess(verify["run"].index("Get-FileHash"), verify["run"].index("python scripts/"))
        for scenario in ("--qualify-v260", "--service-only"):
            self.assertIn(scenario, verify["run"])

    def test_release_downloads_the_bundle_identified_by_the_installer_gate(self):
        installer = workflow("windows-gui-installer")
        self.assertEqual({"build", "qualify"}, set(installer["jobs"]["installer"]["needs"]))
        output = installer["on"]["workflow_call"]["outputs"]["gui_artifact_id"]
        self.assertEqual("${{ jobs.installer.outputs.gui_artifact_id }}", output["value"])
        steps = workflow("release")["jobs"]["release"]["steps"]
        download = next(step for step in steps if step.get("with", {}).get("path") == "bin/gui-release")
        guard = next(step for step in steps if "GUI_ARTIFACT_ID" in step.get("env", {}))
        qualified_id = "${{ needs.gui-installer.outputs.gui_artifact_id }}"
        self.assertEqual(qualified_id, guard["env"]["GUI_ARTIFACT_ID"])
        self.assertLess(steps.index(guard), steps.index(download))
        self.assertEqual({"artifact-ids": qualified_id, "merge-multiple": "true",
                          "path": "bin/gui-release"}, download["with"])

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
