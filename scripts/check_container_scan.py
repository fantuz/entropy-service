#!/usr/bin/env python3
"""Validate Trivy image reports and reject fixable HIGH/CRITICAL findings."""

import argparse
import json


def check_report(report, image_id):
    if report.get("ArtifactType") != "container_image" or report.get("Metadata", {}).get("ImageID") != image_id:
        raise ValueError("scan does not describe the exact tested image")
    if not isinstance(report.get("Results"), list):
        raise ValueError("missing image scan results")
    blockers = []
    for result in report["Results"]:
        findings = result.get("Vulnerabilities") or []
        if not isinstance(findings, list):
            raise ValueError("invalid vulnerability results")
        for finding in findings:
            severity = finding.get("Severity")
            if severity not in {"UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL"}:
                raise ValueError("invalid or missing vulnerability severity")
            if severity in {"HIGH", "CRITICAL"} and finding.get("FixedVersion"):
                blockers.append(f"{finding.get('VulnerabilityID', 'unknown CVE')} {finding.get('PkgName', 'unknown package')}: {severity}, fixed in {finding['FixedVersion']}")
    return blockers


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report")
    parser.add_argument("--image-id", required=True)
    args = parser.parse_args()
    with open(args.report, encoding="utf-8") as stream:
        blockers = check_report(json.load(stream), args.image_id)
    if blockers:
        raise SystemExit("Fixable image vulnerabilities:\n" + "\n".join(blockers))
    print("PASS image identity and fixable HIGH/CRITICAL vulnerability policy:", args.report)


if __name__ == "__main__":
    main()
