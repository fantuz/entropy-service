"""Check optional web catalog staging without downloading the GTK runtime."""
import hashlib
import json
from pathlib import Path
import re
import tempfile
import unittest

from build_windows_gui_bundle import ROOT, stage_ui_locales


class CatalogStagingTests(unittest.TestCase):
    def test_real_catalogs_are_separate_and_byte_identical(self):
        source = ROOT / "cmd/entropy-server/web/locales"
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "bin/ui-locales"
            records = stage_ui_locales(source, destination)
            self.assertEqual(len(records), 14)
            self.assertEqual({record["language"] for record in records},
                             {"en", "fr", "de", "es", "cs", "ru", "pl", "el", "it", "nl", "pt-PT", "pt-BR", "ja", "ar"})
            for record in records:
                body = (destination / record["file"]).read_bytes()
                self.assertEqual(body, (source / record["file"]).read_bytes())
                self.assertEqual(hashlib.sha256(body).hexdigest(), record["sha256"])
            self.assertEqual({path.name for path in destination.iterdir()}, {record["file"] for record in records})

    def test_invalid_input_does_not_stage_a_partial_pack(self):
        for content in (b"not JSON", b"[]", b'{"message":0}', b"x" * (2 * 1024 * 1024 + 1)):
            with self.subTest(content=content[:12]), tempfile.TemporaryDirectory() as directory:
                source, destination = Path(directory) / "catalogs", Path(directory) / "installed"
                source.mkdir()
                (source / "en.json").write_text(json.dumps({"Message": "Message"}))
                (source / "fr.json").write_bytes(content)
                with self.assertRaises(ValueError):
                    stage_ui_locales(source, destination)
                self.assertFalse(destination.exists())

    def test_missing_english_is_a_build_error(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "catalogs"
            source.mkdir()
            (source / "fr.json").write_text('{"Message":"Message"}')
            with self.assertRaisesRegex(ValueError, "English fallback"):
                stage_ui_locales(source, Path(directory) / "installed")

    def test_installer_components_cover_each_catalog_once(self):
        # A new catalog must not silently leak through the recursive core copy
        # or disappear from the component page in the shipping setup recipe.
        recipe = (ROOT / "tools/windows-installer/entropy-service.iss").read_text()
        self.assertIn('Excludes: "bin\\ui-locales\\*"', recipe)
        entries = re.findall(r'Source: "\{#BundleDir\}\\bin\\ui-locales\\([^"\\]+\.json)";[^\n]*Components: ([^;]+);', recipe)
        catalogs = {path.name for path in (ROOT / "cmd/entropy-server/web/locales").glob("*.json")}
        self.assertEqual(len(entries), len(catalogs))
        self.assertEqual({name for name, _ in entries}, catalogs)
        for name, component in entries:
            self.assertEqual(component, "core" if name == "en.json" else "web_locales\\" + name[:-5].replace("-", "_").lower())


if __name__ == "__main__":
    unittest.main()
