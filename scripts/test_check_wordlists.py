"""Reproducible import contracts for native spelling subsets and NFC data."""
import importlib.util
import itertools
from pathlib import Path
import unicodedata
import unittest


SPEC = importlib.util.spec_from_file_location("check_wordlists", Path(__file__).with_name("check-wordlists.py"))
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


def fixture_words(alphabet, count):
    return ["".join(letters) for letters in itertools.islice(itertools.product(alphabet, repeat=6), count)]


def hunspell(words, code):
    # Mirror only the declared upstream file shape, not the selection algorithm.
    count = len(words) + (1 if code == "ta" else 0)
    suffix = "\t1" if code == "ar" else ""
    return (str(count) + suffix + "\n" + "\n".join(words) + "\n").encode("utf-8")


class SpellingSubsetTests(unittest.TestCase):
    def test_exact_membership_filters_and_normalization(self):
        for code, alphabet, extra in [
            ("af", "abcdef", "café"),
            ("ta", "அஆஇஈஉஊ", "காட்டு"),
            ("ne", "कखगघङच", "किताब"),
            ("ar", "بتثجحخ", "كِتَاب"),
        ]:
            with self.subTest(code=code):
                words = fixture_words(alphabet, 8191) + [extra]
                expected = {unicodedata.normalize("NFC", word) for word in words}
                invalid = ["", "a", "ab", "a" * 13, "UPPERCASE", "1234", "two words", "bad\u202eword",
                           "\u0301mark", extra + "\u200d", "x\x00y", "# comment", "latin"]
                # Latin lowercase text is valid for Afrikaans; leave that case
                # out there. No source word is lowercased or translated.
                if code == "af":
                    invalid.remove("latin")
                source = [word + "/AB" for word in words] + [extra, unicodedata.normalize("NFD", extra)] + invalid
                result = CHECKER.convert(hunspell(source, code), f"hunspell-{code}-subset-v1")
                actual = result.decode().splitlines()
                self.assertEqual(len(actual), 8192)
                self.assertEqual(actual, sorted(expected))
                self.assertTrue(result.endswith(b"\n"))

    def test_selection_is_independent_of_source_order_and_duplicates(self):
        words = fixture_words("abcdef", 10000)
        first = CHECKER.convert(hunspell(words, "af"), "hunspell-af-subset-v1")
        reordered = list(reversed(words)) + words[:100]
        second = CHECKER.convert(hunspell(reordered, "af"), "hunspell-af-subset-v1")
        self.assertEqual(first, second)
        selected = first.decode().splitlines()
        self.assertEqual(len(selected), 8192)
        self.assertEqual(len(set(selected)), 8192)
        self.assertTrue(set(selected) < set(words))

    def test_refuses_wrong_record_counts_and_insufficient_candidates(self):
        for code in ["af", "ta", "ne", "ar"]:
            with self.subTest(code=code):
                with self.assertRaises(ValueError):
                    CHECKER.convert(b"7\none\ntwo\n", f"hunspell-{code}-subset-v1")
                with self.assertRaisesRegex(ValueError, "8192"):
                    CHECKER.convert(hunspell(["word"], code), f"hunspell-{code}-subset-v1")
        with self.assertRaises(UnicodeDecodeError):
            CHECKER.convert(b"1\n\xff\n", "hunspell-af-subset-v1")

    def test_plain_data_preserves_order_and_normalizes_nfc(self):
        result = CHECKER.convert("が\n가\n\u00e9\n".encode(), "plain")
        self.assertEqual(result, "が\n가\né\n".encode())

    def test_dice_indexes_must_be_complete_and_ordered(self):
        with self.assertRaises(AssertionError):
            CHECKER.convert(b"11111 first\n11113 skipped\n", "dice-indexed")


if __name__ == "__main__":
    unittest.main()
