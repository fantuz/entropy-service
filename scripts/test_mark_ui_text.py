"""Safety boundaries for explicit interface-text annotations."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("mark_ui_text", Path(__file__).with_name("mark-ui-text.py"))
marker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(marker)


def annotate(source):
    parser = marker.Marker()
    parser.feed(source)
    parser.flush_text()
    return "".join(parser.output), parser.keys


class MarkInterfaceTextTests(unittest.TestCase):
    def test_payloads_routes_and_executable_text_remain_literal(self):
        raw = '''<script>button.textContent = "Raw script";</script><pre>curl /words?lang=it</pre>
<code>GET /bytes</code><textarea>Keep my user text</textarea><canvas>Canvas raw data</canvas>
<div id="words-content">random raw words</div><div id="hash">raw digest</div>
<output id="lab-body-preview">foreign response body</output><p translate="no">User input</p>'''
        result, keys = annotate(raw + '<a href="/words?lang=it">Random words</a><input name="kind" value="random" placeholder="Choose a source">')
        self.assertTrue(result.startswith(raw))
        self.assertNotIn("raw digest", keys)
        self.assertIn('href="/words?lang=it"', result)
        self.assertIn('name="kind" value="random"', result)
        self.assertIn('data-i18n="Random words"', result)
        self.assertIn('data-i18n-placeholder="Choose a source"', result)

    def test_entities_are_one_message_and_annotation_is_idempotent(self):
        first, keys = annotate('<p>Source &amp; sampling</p><button title="Start &quot;now&quot;">Start</button>')
        self.assertIn("Source & sampling", keys)
        self.assertIn('data-i18n="Source &amp; sampling"', first)
        self.assertIn('data-i18n-title="Start &quot;now&quot;"', first)
        second, second_keys = annotate(first)
        self.assertEqual(first, second)
        self.assertEqual(keys, second_keys)

    def test_title_options_and_existing_parameter_bindings_stay_valid(self):
        result, keys = annotate('<title>Source lab</title><select id="kind"><option value="crypto">Browser random bytes</option></select>'
                                '<p data-i18n="Received {bytes} bytes" data-i18n-param-bytes="64">Received 64 bytes</p>')
        self.assertIn('<title data-i18n="Source lab">Source lab</title>', result)
        self.assertIn('<option value="crypto" data-i18n="Browser random bytes">Browser random bytes</option>', result)
        self.assertNotIn('<option value="crypto"><span', result)
        self.assertIn('data-i18n-param-bytes="64">Received 64 bytes</p>', result)
        self.assertIn("Received {bytes} bytes", keys)
        self.assertNotIn("Received 64 bytes", keys)


if __name__ == "__main__":
    unittest.main()
