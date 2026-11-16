import copy
import unittest

from check_container_scan import check_report


class ContainerScanTests(unittest.TestCase):
    image_id = "sha256:" + "a" * 64

    def report(self, findings):
        return {"ArtifactType": "container_image", "Metadata": {"ImageID": self.image_id},
                "Results": [{"Vulnerabilities": findings}]}

    def test_fixable_high_and_critical_block_but_all_findings_are_retained(self):
        report = self.report([{"Severity": "HIGH", "FixedVersion": "2", "VulnerabilityID": "CVE-example", "PkgName": "example"},
                              {"Severity": "CRITICAL", "FixedVersion": "3"},
                              {"Severity": "HIGH", "FixedVersion": ""},
                              {"Severity": "MEDIUM", "FixedVersion": "2"}])
        original = copy.deepcopy(report)
        self.assertEqual(len(check_report(report, self.image_id)), 2)
        self.assertEqual(report, original)

    def test_wrong_image_or_missing_results_fail_closed(self):
        for report, expected_id in [({}, self.image_id),
                                    ({"ArtifactType": "container_image", "Metadata": {"ImageID": self.image_id}}, self.image_id),
                                    (self.report([]), "sha256:" + "b" * 64)]:
            with self.assertRaises(ValueError):
                check_report(report, expected_id)

    def test_malformed_finding_is_not_treated_as_clean_scan(self):
        with self.assertRaises(ValueError):
            check_report(self.report([{}]), self.image_id)


if __name__ == "__main__":
    unittest.main()
