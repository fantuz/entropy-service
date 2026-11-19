#!/usr/bin/env python3
"""Browser target safety for the platform-independent desktop GUI helper."""
from pathlib import Path
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.web import web_application_url


class WebApplicationURLTests(unittest.TestCase):
    def test_origin_mapping_removes_api_routes_and_secrets(self):
        examples = {
            "http://localhost:8080/v1/data/random?bytes=32&token=secret#private": "http://localhost:8080/",
            "https://example.org/ready?key=secret": "https://example.org/",
            "ws://127.0.0.1:8080/stream?bytes=65536": "http://127.0.0.1:8080/",
            "wss://example.org:8443/stream?token=secret#fragment": "https://example.org:8443/",
            "HTTP://EXAMPLE.ORG/path": "http://example.org/",
            "https://bücher.example/path": "https://xn--bcher-kva.example/",
            "http://[2001:db8::7]:8080/v1/data/random": "http://[2001:db8::7]:8080/",
            "wss://[::1]/stream": "https://[::1]/",
        }
        for endpoint, expected in examples.items():
            with self.subTest(endpoint=endpoint):
                self.assertEqual(web_application_url(endpoint), expected)

    def test_invalid_targets_and_credentials_are_rejected_without_echoing_secrets(self):
        invalid = [None, 17, "", "localhost:8080", "//example.org", "http:///missing",
                   "javascript:alert(1)", "file:///etc/passwd", "data:text/html,test", "ftp://example.org/",
                   "http://user:secret@example.org/", "http://user@example.org/", "http://@example.org/",
                   "http://example.org:", "http://example.org:0/", "http://example.org:-1/",
                   "http://example.org:65536/", "http://example.org:secret/",
                   "http://[::1", "http://[::1]:", "http://[::1]:65536", "http://[invalid]/", "http://[::1]suffix/",
                   "http://0.0.0.0/", "http://[::]/", "http://[fe80::1%eth0]/",
                   "http://127.1/", "http://2130706433/", "http://0x7f.1/", "http://127.00.0.1/", "http://example.123/",
                   "http://bad_name/", "http://example..org/", "http://-bad.example/",
                   "http://example%2eorg/", "http://" + "a" * 64 + ".org/",
                   " http://example.org/", "http://example.org/ ", "http://example.org/\n",
                   "http://example.org/path\x00", "http://example.org/\u200b", "http://example.org\\@other/",
                   "http://example.org/" + "a" * 2048]
        for endpoint in invalid:
            with self.subTest(endpoint=endpoint):
                with self.assertRaises(ValueError) as rejected:
                    web_application_url(endpoint)
                self.assertNotIn("secret", str(rejected.exception))

    def test_derivation_has_no_network_or_browser_side_effects(self):
        with mock.patch("socket.getaddrinfo", side_effect=AssertionError("Unexpected DNS lookup")), \
                mock.patch("webbrowser.open", side_effect=AssertionError("Unexpected browser launch")):
            self.assertEqual(web_application_url("https://not-resolved.invalid/api?secret=hidden"),
                             "https://not-resolved.invalid/")


if __name__ == "__main__":
    unittest.main()
