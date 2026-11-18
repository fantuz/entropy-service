import copy
from contextlib import ExitStack
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from oci_bundle import Archive, CONFIG, INDEX, MANIFEST, PREFIX, add_bytes, blob_path, combine, descriptor, encoded

SHA = 'a' * 40
REPOSITORY = 'fantuz/entropy-service'


def fixture_members(role='server', platform='linux-amd64', revision=SHA, version='edge', mutate=None, index_wrap=False):
    arch, variant = {'linux-amd64': ('amd64', ''), 'linux-arm64': ('arm64', ''), 'linux-armv7': ('arm', 'v7')}[platform]
    layer = io.BytesIO()
    with tarfile.open(fileobj=layer, mode='w') as target:
        add_bytes(target, 'fixture-only.txt', b'OCI validation fixture, not an application image')
    plain = layer.getvalue()
    compressed = gzip.compress(plain, mtime=0)
    layer_desc = descriptor(compressed, 'application/vnd.oci.image.layer.v1.tar+gzip')
    config = {'os': 'linux', 'architecture': arch, 'variant': variant,
              'config': {'User': '65532:65532', 'Entrypoint': ['/usr/local/bin/entropy-' + role],
                         'Labels': {PREFIX + 'revision': revision, PREFIX + 'version': version,
                                    PREFIX + 'source': 'https://github.com/' + REPOSITORY}},
              'rootfs': {'type': 'layers', 'diff_ids': ['sha256:' + hashlib.sha256(plain).hexdigest()]}}
    if mutate:
        mutate(config)
    config_raw = encoded(config)
    config_desc = descriptor(config_raw, CONFIG)
    manifest_raw = encoded({'schemaVersion': 2, 'mediaType': MANIFEST, 'config': config_desc, 'layers': [layer_desc]})
    image = descriptor(manifest_raw, MANIFEST, platform={'os': 'linux', 'architecture': arch, 'variant': variant})
    members = {'oci-layout': encoded({'imageLayoutVersion': '1.0.0'}),
               'index.json': encoded({'schemaVersion': 2, 'manifests': [image]}),
               blob_path(image['digest']): manifest_raw, blob_path(config_desc['digest']): config_raw,
               blob_path(layer_desc['digest']): compressed}
    if index_wrap:
        raw = encoded({'schemaVersion': 2, 'mediaType': INDEX, 'manifests': [image]})
        wrapped = descriptor(raw, INDEX)
        members[blob_path(wrapped['digest'])] = raw
        members['index.json'] = encoded({'schemaVersion': 2, 'manifests': [wrapped]})
    return members


def write_fixture(path, members=None, **options):
    members = fixture_members(**options) if members is None else members
    with tarfile.open(path, 'w') as target:
        for name, data in members.items():
            add_bytes(target, name, data)
    return path


class OCIArchiveTests(unittest.TestCase):
    def test_combine_preserves_all_tested_blobs_and_has_stable_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            inputs = [write_fixture(root / (p+'.tar'), platform=p, index_wrap=True) for p in ('linux-amd64', 'linux-arm64', 'linux-armv7')]
            output = root/'server.oci.tar'
            digest = combine(inputs, output, SHA, 'edge', 'server')
            second = root/'again.tar'
            self.assertEqual(combine(inputs, second, SHA, 'edge', 'server'), digest)
            self.assertEqual(output.read_bytes(), second.read_bytes())
            with ExitStack() as stack:
                combined = Archive(output, SHA, 'edge', 'server', stack)
                self.assertEqual(set(combined.images), {'linux-amd64', 'linux-arm64', 'linux-armv7'})
                self.assertEqual(combined.root['digest'], digest)
                for source in inputs:
                    original = Archive(source, SHA, 'edge', 'server', stack)
                    for name in original.image_blobs:
                        self.assertEqual(original.tar.extractfile(original.member(name)).read(),
                                         combined.tar.extractfile(combined.member(name)).read())

    def test_source_version_role_privilege_and_platform_are_checked(self):
        for mutate in (lambda c: c['config']['Labels'].update({PREFIX+'revision': 'b'*40}),
                       lambda c: c['config']['Labels'].update({PREFIX+'version': 'old'}),
                       lambda c: c['config'].update(User='0:0'),
                       lambda c: c['config'].update(Entrypoint=['/usr/local/bin/entropy-client']),
                       lambda c: c.update(architecture='amd64', os='windows')):
            with tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
                path = write_fixture(Path(directory)/'bad.tar', mutate=mutate)
                with self.assertRaises(ValueError):
                    Archive(path, SHA, 'edge', 'server', stack)

    def test_missing_or_duplicate_platform_cannot_be_combined(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = write_fixture(root/'first.tar')
            for inputs in ([first], [first, first]):
                with self.subTest(inputs=inputs), self.assertRaises(ValueError):
                    combine(inputs, root/'out.tar', SHA, 'edge', 'server')

    def test_digest_size_missing_and_unreferenced_blob_rejection(self):
        original = fixture_members()
        layer = next(name for name, data in original.items() if data.startswith(b'\x1f\x8b'))
        for mutation in ('digest', 'size', 'missing', 'unreferenced'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
                members = copy.deepcopy(original)
                if mutation == 'digest': members[layer] = b'x' * len(members[layer])
                elif mutation == 'size': members[layer] += b'x'
                elif mutation == 'missing': del members[layer]
                else: members['blobs/sha256/'+'0'*64] = b'unreferenced'
                path = write_fixture(Path(directory)/'bad.tar', members)
                with self.assertRaises(ValueError): Archive(path, SHA, 'edge', 'server', stack)

    def test_paths_links_duplicates_and_unexpected_files_are_rejected_without_extraction(self):
        for name in ('../escape', '/absolute', 'blobs/../escape', 'blobs\\escape', 'private-key.pem', 'index.json'):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
                path = Path(directory)/'bad.tar'
                write_fixture(path)
                with tarfile.open(path, 'a') as target: add_bytes(target, name, b'bad')
                with self.assertRaises(ValueError): Archive(path, SHA, 'edge', 'server', stack)
                self.assertFalse((Path(directory)/'escape').exists())
        with tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
            path = write_fixture(Path(directory)/'link.tar')
            with tarfile.open(path, 'a') as target:
                info = tarfile.TarInfo('blobs/sha256/'+'0'*64)
                info.type, info.linkname = tarfile.SYMTYPE, '/etc/passwd'
                target.addfile(info)
            with self.assertRaises(ValueError): Archive(path, SHA, 'edge', 'server', stack)

    def test_external_descriptor_is_not_fetched(self):
        members = fixture_members()
        index = json.loads(members['index.json']);index['manifests'][0]['urls'] = ['https://example.org/private']
        members['index.json'] = encoded(index)
        with tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
            path = write_fixture(Path(directory)/'external.tar', members)
            with self.assertRaisesRegex(ValueError, 'External'):
                Archive(path, SHA, 'edge', 'server', stack)


if __name__ == '__main__':
    unittest.main()
