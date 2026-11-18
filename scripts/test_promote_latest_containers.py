import json
import unittest
from unittest.mock import call, patch

import promote_latest_containers as promotion


class LatestPromotionTests(unittest.TestCase):
    repository = "fantuz/entropy-service"
    old = ("v2.3.0", "a" * 40)
    new = ("v2.4.0", "b" * 40)
    images = [("ghcr.io/fantuz/entropy-service", "sha256:" + "c" * 64),
              ("ghcr.io/fantuz/entropy-client", "sha256:" + "d" * 64)]

    @patch.object(promotion, "docker")
    def test_duplicate_platform_manifest_is_rejected(self, docker):
        manifests = [{"platform": p} for p in [{"os": "linux", "architecture": "amd64"},
                                               {"os": "linux", "architecture": "arm64"},
                                               {"os": "linux", "architecture": "arm", "variant": "v7"}]]
        docker.side_effect = [json.dumps({"digest": "sha256:" + "a" * 64}).encode(),
                              json.dumps({"manifests": manifests + [manifests[0]]}).encode()]
        with self.assertRaisesRegex(ValueError, "exactly the three"):
            promotion.inspect_index("ghcr.io/fantuz/entropy-service:v2.4.0")

    @patch.object(promotion, "inspect_index")
    @patch.object(promotion, "docker")
    @patch.object(promotion, "prepare_images")
    @patch.object(promotion, "latest_release")
    def test_queued_older_backfill_reconciles_current_release(self, latest, prepare, docker, inspect):
        latest.return_value = self.new
        prepare.return_value = self.images
        inspect.side_effect = [(digest, {}) for _, digest in self.images]
        promotion.synchronise_latest(self.repository)
        prepare.assert_called_once_with(self.repository, self.new)
        self.assertEqual(docker.call_count, 2)
        for destination, digest in self.images:
            self.assertIn(call("buildx", "imagetools", "create", "--tag", destination + ":latest", destination + "@" + digest), docker.call_args_list)

    @patch.object(promotion, "inspect_index")
    @patch.object(promotion, "docker")
    @patch.object(promotion, "prepare_images")
    @patch.object(promotion, "latest_release")
    def test_release_changed_during_preflight_never_promotes_old_image(self, latest, prepare, docker, inspect):
        latest.side_effect = [self.old] + [self.new] * 6
        prepare.return_value = self.images
        inspect.side_effect = [(digest, {}) for _, digest in self.images]
        promotion.synchronise_latest(self.repository)
        self.assertEqual(prepare.call_args_list, [call(self.repository, self.old), call(self.repository, self.new)])
        self.assertEqual(docker.call_count, 2)

    @patch.object(promotion, "inspect_index")
    @patch.object(promotion, "docker")
    @patch.object(promotion, "prepare_images")
    @patch.object(promotion, "latest_release")
    def test_release_changes_during_last_registry_write_is_reconciled(self, latest, prepare, docker, inspect):
        latest.side_effect = [self.old] * 4 + [self.new] * 6
        prepare.return_value = self.images
        inspect.side_effect = [(digest, {}) for _, digest in self.images] * 2
        promotion.synchronise_latest(self.repository)
        self.assertEqual(prepare.call_args_list, [call(self.repository, self.old), call(self.repository, self.new)])
        self.assertEqual(docker.call_count, 4)

    @patch.object(promotion, "docker")
    @patch.object(promotion, "prepare_images", side_effect=ValueError("wrong source label"))
    @patch.object(promotion, "latest_release")
    def test_both_images_are_validated_before_any_latest_write(self, latest, prepare, docker):
        latest.return_value = self.new
        with self.assertRaisesRegex(ValueError, "source"):
            promotion.synchronise_latest(self.repository)
        docker.assert_not_called()

    @patch.object(promotion, "github_json")
    def test_annotated_release_tag_is_peeled_to_commit(self, api):
        api.side_effect = [{"tag_name": "v2.4.0", "draft": False, "prerelease": False, "published_at": "2026-11-16"},
                           {"object": {"type": "tag", "sha": "c" * 40}},
                           {"object": {"type": "commit", "sha": self.new[1]}}]
        self.assertEqual(promotion.latest_release(self.repository), self.new)

    @patch.object(promotion, "github_json")
    def test_prerelease_or_unpublished_release_cannot_be_latest(self, api):
        for release in [{"tag_name": "v2.4.0-rc.1", "prerelease": True},
                        {"tag_name": "v2.4.0", "draft": True},
                        {"tag_name": "v2.4.0"}]:
            api.return_value = release
            with self.assertRaises(ValueError):
                promotion.latest_release(self.repository)


if __name__ == "__main__":
    unittest.main()
