import os
import unittest
from unittest.mock import MagicMock, patch
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlsplit

from ci_cache_cleanup import GitHub, candidates, cleanup, list_caches, main


def cache(identifier, ref="refs/heads/main", profile="quality"):
    return {"id": identifier, "ref": ref, "created_at": f"2026-11-{identifier:02d}T00:00:00Z",
            "key": f"go-build-v2-Linux-X64-{profile}-1.26.11-{'a' * 64}-{'b' * 64}"}


class API:
    def __init__(self, caches):
        self.caches = caches
        self.deleted = []

    def __call__(self, path, method="GET"):
        if not path:
            return {"default_branch": "main"}
        if method == "DELETE":
            self.deleted.append(path)
            return None
        query = parse_qs(urlsplit(path).query)
        page = int(query["page"][0])
        if query["sort"] != ["created_at"] or query["direction"] != ["asc"]:
            raise ValueError("Cache listing must use stable creation order")
        batch = self.caches[(page - 1) * 100:page * 100]
        return {"total_count": len(self.caches) if batch else 0, "actions_caches": batch}

    def pr_state(self, number):
        return {1: "closed", 2: "open"}.get(number, "unknown")


class CleanupTests(unittest.TestCase):
    def test_closed_pr_only_and_two_main_snapshots_per_profile(self):
        items = [cache(i) for i in range(1, 5)]
        items += [cache(5, profile="race"), cache(6, "refs/pull/1/merge"),
                  cache(7, "refs/pull/2/head"), cache(8, "refs/pull/3/merge"),
                  cache(9, "refs/heads/feature"),
                  dict(cache(10), key="buildkit-unrelated")]
        result = candidates(items, API([]).pr_state)
        self.assertEqual([1, 2, 6], [item["id"] for item in result])
        self.assertEqual([6], [item["id"] for item in candidates(items, API([]).pr_state, closed_pr=1)])

    def test_dry_run_default_never_deletes_and_apply_is_bounded(self):
        api = API([cache(i, "refs/pull/1/merge") for i in range(1, 6)])
        self.assertEqual(5, cleanup(api))
        self.assertEqual([], api.deleted)
        self.assertEqual(2, cleanup(api, dry_run=False, max_delete=2))
        self.assertEqual(["actions/caches/1", "actions/caches/2"], api.deleted)

    def test_complete_pagination_before_deletion(self):
        api = API([cache(i, "refs/pull/1/head") for i in range(1, 102)])
        self.assertEqual(101, len(list_caches(api)))
        original = api.__class__.__call__

        class Incomplete(API):
            def __call__(self, path, method="GET"):
                if "page=2" in path:
                    raise RuntimeError("API listing failed")
                return original(self, path, method)

        api = Incomplete(api.caches)
        with self.assertRaises(RuntimeError):
            cleanup(api, dry_run=False)
        self.assertEqual([], api.deleted)

    def test_inconsistent_listing_or_state_error_never_deletes(self):
        with self.assertRaises(ValueError):
            list_caches(lambda path: {"total_count": 2, "actions_caches": [cache(1)]})

        class FailedState(API):
            def pr_state(self, number):
                if number == 2:
                    raise RuntimeError("API state failed")
                return "closed"

        api = FailedState([cache(1, "refs/pull/1/merge"), cache(2, "refs/pull/2/merge")])
        with self.assertRaises(RuntimeError):
            cleanup(api, dry_run=False)
        self.assertEqual([], api.deleted)

    def test_repeated_cache_during_pagination_never_deletes(self):
        class Repeated(API):
            def __call__(self, path, method="GET"):
                if "page=2&" in path:
                    return {"total_count": 101, "actions_caches": [self.caches[0]]}
                return super().__call__(path, method)

        api = Repeated([cache(i, "refs/pull/1/head") for i in range(1, 102)])
        with self.assertRaises(ValueError):
            cleanup(api, dry_run=False)
        self.assertEqual([], api.deleted)

    def test_empty_terminating_page_can_report_zero_total(self):
        api = API([cache(i, "refs/pull/1/head") for i in range(1, 1001)])
        self.assertEqual(1000, len(list_caches(api)))

    def test_invalid_deletion_bound(self):
        with self.assertRaises(ValueError):
            cleanup(API([]), dry_run=False, max_delete=201)

    def test_repository_metadata_url_has_no_trailing_slash(self):
        api = GitHub("example/repository", "redacted-test-token")
        response = MagicMock()
        response.__enter__.return_value.read.return_value = b'{"default_branch":"main"}'
        with patch("ci_cache_cleanup.urlopen", return_value=response) as opened:
            self.assertEqual("main", api("")["default_branch"])
            self.assertEqual("https://api.github.com/repos/example/repository", opened.call_args.args[0].full_url)
            api("actions/caches?per_page=100&page=1")
            self.assertEqual("https://api.github.com/repos/example/repository/actions/caches?per_page=100&page=1",
                             opened.call_args.args[0].full_url)

    def test_forbidden_or_unknown_prs_are_retained(self):
        api = GitHub("example/repository", "redacted-test-token")
        for code in (403, 404):
            with patch("ci_cache_cleanup.urlopen", side_effect=HTTPError("test", code, "unknown", {}, None)):
                self.assertEqual("unknown", api.pr_state(1))

    def test_cli_requires_explicit_apply(self):
        for args, expected in (([], 0), (["--dry-run"], 0), (["--apply"], 1)):
            api = API([cache(1, "refs/pull/1/merge")])
            with patch("ci_cache_cleanup.GitHub", return_value=api), \
                 patch.dict(os.environ, GH_TOKEN="redacted-test-token", GITHUB_REPOSITORY="example/repository"), \
                 patch("sys.argv", ["ci_cache_cleanup.py", *args]):
                main()
            self.assertEqual(expected, len(api.deleted))
