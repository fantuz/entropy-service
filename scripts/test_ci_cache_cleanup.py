import unittest

from ci_cache_cleanup import candidates, cleanup, list_caches


def cache(identifier, ref="refs/heads/main", profile="quality"):
    return {"id": identifier, "ref": ref, "created_at": f"2026-11-{identifier:02d}T00:00:00Z",
            "key": f"go-build-v2-Linux-X64-{profile}-1.26.11-{'a' * 64}-{'b' * 64}"}


class API:
    def __init__(self, caches):
        self.caches = caches
        self.deleted = []

    def __call__(self, path, method="GET"):
        if method == "DELETE":
            self.deleted.append(path)
            return None
        page = int(path.rsplit("=", 1)[1])
        return {"total_count": len(self.caches), "actions_caches": self.caches[(page - 1) * 100:page * 100]}

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

    def test_invalid_deletion_bound(self):
        with self.assertRaises(ValueError):
            cleanup(API([]), dry_run=False, max_delete=201)
