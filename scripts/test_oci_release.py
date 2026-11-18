"""Validate offline artifact evidence, publication boundaries and release assets."""
from contextlib import ExitStack
import json
from pathlib import Path
import tempfile
import unittest

from oci_bundle import Archive
from oci_release import PLATFORMS, ROLES, build, native_inputs, verify, write_checksums
from prepare_gui_release import release_config
from publish_oci import registry_options
from test_oci_bundle import SHA, REPOSITORY, write_fixture


def inputs(root, version='edge'):
    """Synthetic scan/SBOM fixtures for unit tests only, never qualification."""
    for platform in PLATFORMS:
        folder = root/('container-images-'+platform)
        folder.mkdir(parents=True)
        for role in ROLES:
            path = folder/(role+'.oci.tar')
            write_fixture(path, role=role, platform=platform, version=version)
            with ExitStack() as stack:
                image = Archive(path, SHA, version, role, stack).images[platform]
            (folder/(role+'.spdx.json')).write_text(json.dumps({'SPDXID': 'SPDXRef-DOCUMENT', 'spdxVersion': 'SPDX-2.3', 'packages': [{'name': 'unit fixture'}]}))
            (folder/(role+'.vulnerabilities.json')).write_text(json.dumps({'ArtifactType': 'container_image', 'Metadata': {'ImageID': image['config_digest']}, 'Results': []}))
        write_checksums(folder, [role+'.oci.tar' for role in ROLES])
        write_checksums(folder, [role+suffix for role in ROLES for suffix in ('.spdx.json', '.vulnerabilities.json')])
        (folder/'checksums.txt').rename(folder/'security-checksums.txt')
        write_checksums(folder, [role+'.oci.tar' for role in ROLES])


class ReleaseTests(unittest.TestCase):
    def test_complete_exact_evidence_and_relative_release_assets(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary); native=root/'native'; output=root/'offline'
            inputs(native, '2.5.1')
            metadata=build(native,output,SHA,'2.5.1',REPOSITORY)
            self.assertEqual(verify(output,SHA,'2.5.1',REPOSITORY,native_inputs(native,SHA,'2.5.1',REPOSITORY)),metadata)
            base=(Path(__file__).resolve().parents[1]/'.goreleaser.yml').read_text()
            config=release_config(base,[output/image['file'] for image in metadata['images'].values()],root)
            self.assertNotIn(str(root),config)
            for image in metadata['images'].values():
                self.assertEqual(config.count('name_template: '+json.dumps(Path(image['file']).name)),2)
            for image in metadata['images'].values():
                self.assertEqual(set(image['platforms']),set(PLATFORMS))

    def test_corrupt_reports_wrong_source_and_changed_metadata_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);native=root/'native';output=root/'offline'
            inputs(native);build(native,output,SHA,'edge',REPOSITORY)
            with self.assertRaises(ValueError):verify(output,'b'*40,'edge',REPOSITORY)
            data=json.loads((output/'oci-images.json').read_text());data['images']['client']['digest']='sha256:'+'0'*64
            (output/'oci-images.json').write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError,'metadata'):verify(output,SHA,'edge',REPOSITORY)
            (native/'container-images-linux-amd64'/'server.spdx.json').write_text('corrupt')
            with self.assertRaisesRegex(ValueError,'checksum'):native_inputs(native,SHA,'edge',REPOSITORY)

    def test_symlink_archive_and_missing_platform_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary);inputs(root)
            path=root/'container-images-linux-arm64'/'client.oci.tar'
            path.rename(path.with_suffix('.backup'));path.symlink_to(path.with_suffix('.backup'))
            with self.assertRaisesRegex(ValueError,'regular'):native_inputs(root,SHA,'edge',REPOSITORY)
            path.unlink()
            with self.assertRaises(ValueError):native_inputs(root,SHA,'edge',REPOSITORY)

    def test_plaintext_only_loopback_and_production_only_ghcr(self):
        with tempfile.TemporaryDirectory() as temporary:
            auth=Path(temporary)/'auth.json';auth.write_text('{}')
            registry_options('127.0.0.1:1234',True,auth)
            registry_options('ghcr.io',False,auth)
            for registry,local in [('example.org:1234',True),('localhost:1234',True),('127.0.0.1:1234/path',True),('127.0.0.1:1234',False),('ghcr.io',True)]:
                with self.subTest(registry=registry),self.assertRaises(ValueError):registry_options(registry,local,auth)


if __name__=='__main__':unittest.main()
