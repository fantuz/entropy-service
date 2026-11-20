#!/usr/bin/env python3
"""Preservation and safety checks for the desktop startup-options editor."""
from dataclasses import FrozenInstanceError
from pathlib import Path
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.options import edited_arguments, fields_for, initial_values


class StartupOptionsTests(unittest.TestCase):
    def test_binary_inventory_supplies_available_backends(self):
        for role, flag, advertised, expected in (
                ("server", "drbg", {"aes-256-ctr": True, "rdrand-experimental": False,
                                    "barzakh-521-experimental": True}, ("aes-256-ctr", "barzakh-521-experimental")),
                ("client", "mode", {"pull": True}, ("pull",))):
            fields = fields_for(role, {flag: advertised, "output": {"file": True}})
            self.assertEqual(next(f.choices for f in fields if f.name == flag), expected)
            self.assertNotIn("output", [f.name for f in fields])

    def test_transport_budgets_use_cli_values_and_preserve_unedited_arguments(self):
        settings = {"http_max_requests": 5, "http_memory_mib": 7, "ws_max_connections": 11, "ws_memory_mib": 13}
        arguments = ["--ws-memory-mib", "19", "-lab-memory-mib=3"]
        values = initial_values("server", arguments, {"settings": settings})
        self.assertEqual([values[name.replace("_", "-")] for name in settings], ["5", "7", "11", "19"])
        self.assertEqual(edited_arguments("server", arguments, {"ws-memory-mib": "23"}),
                         ["-lab-memory-mib=3", "-ws-memory-mib=23"])

    def test_adaptive_checkbox_reads_boolean_from_descriptor(self):
        description = {"settings": {"adaptive": True}}
        self.assertIs(initial_values("client", [], description)["adaptive"], True)
        self.assertIs(initial_values("client", ["-adaptive=false"], description)["adaptive"], False)

    def test_explicit_experimental_cpu_backend_is_preserved(self):
        field = next(field for field in fields_for("server") if field.name == "drbg")
        self.assertIn("rdrand-experimental", field.choices)
        self.assertEqual(edited_arguments("server", [], {"drbg": "rdrand-experimental"}),
                         ["-drbg=rdrand-experimental"])

    def test_macos_defaults_match_os_source_and_cpu_architecture(self):
        for arch, backend in (("arm64", "chacha20"), ("x86_64", "aes-256-ctr")):
            with mock.patch("entropy_gui.options.sys.platform", "darwin"), mock.patch("entropy_gui.options.platform.machine", return_value=arch):
                values = initial_values("server", [])
                self.assertEqual(values["device"], "macos-csprng")
                self.assertEqual(values["drbg"], backend)

    def test_field_allowlists_and_immutable_metadata(self):
        client = fields_for("client")
        self.assertEqual(len({field.name for field in client}), len(client))
        self.assertTrue({"mode", "url", "wsurl", "slice", "shannon-threshold"} <= {field.name for field in client})
        self.assertTrue({"device", "drbg", "cert-file", "key-file", "on-reseed-failure"} <=
                        {field.name for field in fields_for("server")})
        self.assertTrue(all(field.kind in ("text", "choice", "bool", "path") for field in client + fields_for("server")))
        with self.assertRaises(FrozenInstanceError):
            client[0].name = "report"
        for role in ("future", None):
            with self.subTest(role=role), self.assertRaises(ValueError):
                fields_for(role)

    def test_raw_urls_zeroes_and_aliases_win_over_redacted_normalized_description(self):
        http = "https://operator:secret@example.org/custom?token=private&bytes=17#fragment"
        websocket = "wss://other:password@example.org/stream?token=second"
        arguments = ["--mode", "websocket", "-url", http, "--wsurl=" + websocket,
                     "-analysis-bytes=0", "-analysis-interval", "0", "-duration=0"]
        description = {"settings": {"mode": "stream", "endpoint": "wss://example.org/stream",
                                    "analysis_bytes": 1048576, "analysis_interval": "250ms", "duration": "0s"},
                       "features": {}}
        values = initial_values("client", arguments, description)
        self.assertEqual(values["mode"], "push")
        self.assertEqual(values["url"], http)
        self.assertEqual(values["wsurl"], websocket)
        for flag in ("analysis-bytes", "analysis-interval", "duration"):
            self.assertEqual(values[flag], "0")
        self.assertEqual(edited_arguments("client", arguments, {}), arguments)
        changed = edited_arguments("client", arguments, {"slice": "4096"})
        self.assertEqual(changed[:-1], arguments)
        self.assertEqual(changed[-1], "-slice=4096")

    def test_server_raw_paths_automatic_zero_and_custom_choice_are_retained(self):
        arguments = ["-cert-file", "C:/TLS material/public cert.pem", "--key-file=C:/Private keys/key.pem",
                     "-max-reseed-age=0", "-drbg=barzakh-521-experimental", "-require-device", "-enable-https=False"]
        values = initial_values("server", arguments, {
            "settings": {"drbg": "aes-256-ctr", "max_reseed_age_ms": 60000, "reseed_interval_ms": 15000},
            "features": {"https": True, "require_device": False, "cpu_mix": True},
        })
        self.assertEqual(values["cert-file"], "C:/TLS material/public cert.pem")
        self.assertEqual(values["key-file"], "C:/Private keys/key.pem")
        self.assertEqual(values["max-reseed-age"], "0")
        self.assertEqual(values["drbg"], "barzakh-521-experimental")
        self.assertIs(values["require-device"], True)
        self.assertIs(values["enable-https"], False)
        self.assertIs(values["cpu-mix"], True)

    def test_description_defaults_use_selected_transport_and_native_server_defaults(self):
        values = initial_values("client", [], {"settings": {"mode": "stream", "endpoint": "wss://custom.example/stream",
                                                            "payload_bytes": 1234, "shannon_threshold_bits_per_byte": 7.999123},
                                                "features": {}})
        self.assertEqual(values["mode"], "push")
        self.assertEqual(values["wsurl"], "wss://custom.example/stream")
        self.assertEqual(values["url"], "http://127.0.0.1:8080/v1/data/random")
        self.assertEqual(values["slice"], "1234")
        self.assertEqual(values["shannon-threshold"], "7.999123")
        with mock.patch("entropy_gui.options.sys.platform", "win32"), mock.patch("entropy_gui.options.platform.machine", return_value="AMD64"):
            self.assertEqual(initial_values("server", [])["device"], "windows-csprng")
            self.assertEqual(initial_values("server", [])["drbg"], "aes-256-ctr")
        with mock.patch("entropy_gui.options.sys.platform", "linux"), mock.patch("entropy_gui.options.platform.machine", return_value="aarch64"):
            self.assertEqual(initial_values("server", [])["device"], "/dev/qrandom0")
            self.assertEqual(initial_values("server", [])["drbg"], "chacha20")

    def test_changed_flag_duplicates_are_removed_without_rewriting_other_arguments(self):
        url = "https://u:secret@host/data?token=a%20b&bytes=99"
        arguments = ["--url", url, "-slice=100", "--slice", "200", "-display-interval", "55ms", "-preview=false"]
        original = list(arguments)
        self.assertEqual(edited_arguments("client", arguments, {"slice": "300"}),
                         ["--url", url, "-display-interval", "55ms", "-preview=false", "-slice=300"])
        self.assertEqual(arguments, original)

    def test_scalar_option_values_that_look_like_flags_stay_opaque(self):
        arguments = ["-lab-sources", "-http=literal-registry-name.json", "--http", "127.0.0.1:8000", "-lab-memory-mib=16"]
        expected = ["-lab-sources", "-http=literal-registry-name.json", "-lab-memory-mib=16", "-http=127.0.0.1:9000"]
        self.assertEqual(edited_arguments("server", arguments, {"http": "127.0.0.1:9000"}), expected)

    def test_lab_debug_and_cookie_edits_preserve_private_paths_and_unrelated_flags(self):
        arguments = ["-lab-sources", "C:/Private registries/lab sources.json", "-lab-allowed-hosts=lab.example",
                     "-debug", "-cookie=false", "--debug=false", "-lab-allow-remote", "-words=3"]
        values = initial_values("server", arguments, {
            "settings": {"lab_max_connections": 2, "lab_memory_mib": 4},
            "features": {"debug": True, "cookie": True, "lab_allow_remote": False},
        })
        self.assertFalse(values["debug"])
        self.assertFalse(values["cookie"])
        self.assertTrue(values["lab-allow-remote"])
        self.assertEqual(values["lab-max-connections"], "2")
        self.assertEqual(values["lab-memory-mib"], "4")
        self.assertEqual(values["lab-sources"], arguments[1])
        self.assertEqual(edited_arguments("server", arguments, {"debug": True, "cookie": True}),
                         ["-lab-sources", arguments[1], "-lab-allowed-hosts=lab.example",
                          "-lab-allow-remote", "-words=3", "-debug=true", "-cookie=true"])
        self.assertEqual(edited_arguments("server", arguments, {"lab-sources": "", "lab-allow-remote": False})[-2:],
                         ["-lab-sources=", "-lab-allow-remote=false"])

    def test_optional_server_feature_defaults_are_portable_and_explicit_reports_win(self):
        for target in ("linux", "win32", "darwin"):
            with self.subTest(platform=target), mock.patch("entropy_gui.options.sys.platform", target):
                values = initial_values("server", [])
                self.assertFalse(values["debug"] or values["cookie"] or values["lab-allow-remote"])
                self.assertEqual(values["lab-sources"], "")
                configured = initial_values("server", [], {"settings": {},
                    "features": {"debug": True, "cookie": True, "lab_allow_remote": True}})
                self.assertTrue(configured["debug"] and configured["cookie"] and configured["lab-allow-remote"])

    def test_bare_boolean_flags_never_consume_the_following_option(self):
        arguments = ["-enable-https", "-http", "127.0.0.1:8000", "-lab-allow-remote", "-cpu-mix=false"]
        self.assertEqual(edited_arguments("server", arguments, {"enable-https": False, "http": "127.0.0.1:9000"}),
                         ["-lab-allow-remote", "-cpu-mix=false", "-enable-https=false", "-http=127.0.0.1:9000"])
        self.assertEqual(initial_values("server", arguments)["http"], "127.0.0.1:8000")
        self.assertIs(initial_values("server", arguments)["enable-https"], True)

    def test_negative_split_values_are_removed_as_values_not_flags(self):
        arguments = ["-analysis-bytes", "-1", "-retries", "0"]
        self.assertEqual(edited_arguments("client", arguments, {"analysis-bytes": "0"}),
                         ["-retries", "0", "-analysis-bytes=0"])

    def test_values_are_single_literal_argv_items_without_shell_evaluation(self):
        value = "https://host/data?literal=$(touch marker); x=-report=-"
        self.assertEqual(edited_arguments("client", [], {"url": value}), ["-url=" + value])
        self.assertEqual(edited_arguments("server", [], {"cert-file": "C:/TLS files/certificate name.pem"}),
                         ["-cert-file=C:/TLS files/certificate name.pem"])

    def test_unexposed_managed_or_malformed_edits_are_rejected(self):
        for role in ("server", "client"):
            for name in ("report", "describe-json", "headless", "output", "output-device", "writeout", "--url", None):
                with self.subTest(role=role, name=name), self.assertRaises(ValueError):
                    edited_arguments(role, [], {name: "value"})
            for arguments in (["--"], ["-help"], ["-describe-json=false"], ["bad\x00argument"]):
                with self.subTest(role=role, arguments=arguments), self.assertRaises(ValueError):
                    edited_arguments(role, arguments, {})
        for updates in ({"url": "x\x00y"}, {"url": "--"}, {"url": True}, {"slice": 256}):
            with self.subTest(updates=updates), self.assertRaises(ValueError):
                edited_arguments("client", [], updates)
        for value in ("yes", 1, None):
            with self.subTest(value=value), self.assertRaises(ValueError):
                edited_arguments("server", [], {"enable-https": value})

    def test_gui_managed_original_client_flags_cannot_be_reintroduced_by_editing(self):
        for flag in ("-report=private.jsonl", "-output=sample.bin", "-writeout=false", "-debug"):
            with self.subTest(flag=flag), self.assertRaises(ValueError):
                edited_arguments("client", [flag], {"slice": "1024"})


if __name__ == "__main__":
    unittest.main()
