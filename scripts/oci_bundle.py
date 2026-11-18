#!/usr/bin/env python3
"""Validate and combine tested OCI archives without rebuilding their image blobs.

Adapted from fantuz/DNSProxy packaging/container/oci_bundle.py (MIT).
The upstream copyright/license is retained in resources/licenses/DNSProxy-OCI.txt.
"""
import argparse
from contextlib import ExitStack
import hashlib
import io
import json
from pathlib import Path
import re
import tarfile

from container_identity import PLATFORMS, verify_image

INDEX = 'application/vnd.oci.image.index.v1+json'
MANIFEST = 'application/vnd.oci.image.manifest.v1+json'
CONFIG = 'application/vnd.oci.image.config.v1+json'
SOURCE = 'https://github.com/fantuz/entropy-service'
PREFIX = 'org.opencontainers.image.'


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def descriptor(data, media_type, **extra):
    return dict(mediaType=media_type, size=len(data),
                digest='sha256:' + hashlib.sha256(data).hexdigest(), **extra)


def blob_path(digest):
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
        raise ValueError('Expected a SHA256 OCI digest')
    return 'blobs/sha256/' + digest[7:]


class Archive:
    def __init__(self, path, revision, version, role, stack, repository="fantuz/entropy-service"):
        self.role, self.repository = role, repository
        if role not in ("server", "client"):
            raise ValueError("Expected server or client image role")
        if not re.fullmatch(r"[0-9a-f]{40}", revision) or not re.fullmatch(r"(?:edge|dev|[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)", version):
            raise ValueError("Expected an immutable source revision and supported version")
        path = Path(path)
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 2 * 1024**3:
            raise ValueError("Expected a regular OCI archive of at most 2 GiB")
        self.tar = stack.enter_context(tarfile.open(path, 'r:'))
        self.members = {}
        for member in self.tar:
            name = member.name.removeprefix('./').rstrip('/')
            if member.isdir() and name in ('', '.'):
                continue
            if (member.name.startswith('/') or '\\' in name or '..' in name.split('/')
                    or not (name in ('oci-layout', 'index.json', 'blobs', 'blobs/sha256')
                            or re.fullmatch(r'blobs/sha256/[0-9a-f]{64}', name))
                    or (not member.isfile() and not member.isdir())
                    or (member.isdir() and name not in ('blobs', 'blobs/sha256'))):
                raise ValueError(f'Unsafe or unexpected OCI archive member: {member.name}')
            if len(self.members) >= 8192:
                raise ValueError('Too many OCI archive members')
            if name in self.members:
                raise ValueError(f'Duplicate archive member: {name}')
            self.members[name] = member
        self.blobs = {}
        layout = self.read_json('oci-layout')
        if layout != {'imageLayoutVersion': '1.0.0'}:
            raise ValueError('Unsupported OCI layout')
        root = self.read_json('index.json')
        if root.get('schemaVersion') != 2 or len(root.get('manifests', [])) != 1:
            raise ValueError('Expected exactly one named image in the OCI archive')
        self.root = root['manifests'][0]
        self.images = {}
        self.image_blobs = set()
        self.walk(self.root, revision, version)
        if {name for name in self.members if name.startswith('blobs/sha256/')} != set(self.blobs):
            raise ValueError('Unreferenced blobs in OCI archive')

    def member(self, name):
        member = self.members.get(name)
        if member is None or not member.isfile():
            raise ValueError(f'Missing regular OCI file: {name}')
        return member

    def read_json(self, name):
        member = self.member(name)
        if member.size > 2 * 1024 * 1024:
            raise ValueError('OCI JSON metadata is too large')
        with self.tar.extractfile(member) as source:
            return json.load(source)

    def verify_blob(self, desc):
        if desc.get('urls') or 'data' in desc or type(desc.get('size')) is not int or desc['size'] < 0:
            raise ValueError('External/inline blobs or invalid descriptor size are unsupported')
        path = blob_path(desc['digest'])
        member = self.member(path)
        if member.size != desc['size']:
            raise ValueError(f'OCI blob size mismatch: {path}')
        if path not in self.blobs:
            digest = hashlib.sha256()
            with self.tar.extractfile(member) as source:
                for chunk in iter(lambda: source.read(1024 * 1024), b''):
                    digest.update(chunk)
            if 'sha256:' + digest.hexdigest() != desc['digest']:
                raise ValueError(f'OCI blob digest mismatch: {path}')
            self.blobs[path] = member
        return path

    def walk(self, desc, revision, version, depth=0):
        path = self.verify_blob(desc)
        document = self.read_json(path)
        if document.get('schemaVersion') != 2:
            raise ValueError('Unsupported OCI manifest schema')
        media_type = desc['mediaType']
        if media_type == INDEX:
            if depth or not document.get('manifests') or len(document['manifests']) > 3 or document.get('mediaType') != INDEX:
                raise ValueError('Unexpected nested or empty OCI index')
            for child in document['manifests']:
                self.walk(child, revision, version, depth + 1)
            return
        if media_type != MANIFEST or document.get('mediaType') != MANIFEST:
            raise ValueError('Expected an OCI image manifest')
        config_desc = document['config']
        if config_desc['mediaType'] != CONFIG:
            raise ValueError('Expected an OCI image configuration')
        config = self.read_json(self.verify_blob(config_desc))
        self.image_blobs.update((path, blob_path(config_desc['digest'])))
        arch = config.get('architecture')
        platform = desc.get('platform', {'os': config.get('os'), 'architecture': arch, 'variant': config.get('variant', '')})
        variant = config.get('variant', platform.get('variant', ''))
        key = next((key for key, (expected_arch, expected_variant) in PLATFORMS.items()
                    if arch == expected_arch and (not expected_variant or variant == expected_variant)), None)
        if key is None or config.get('os') != 'linux':
            raise ValueError('Unsupported container platform')
        if key in self.images:
            raise ValueError(f'Duplicate platform: {key}')
        if (platform.get('os') != 'linux' or platform.get('architecture') != arch
                or (arch == 'arm' and platform.get('variant') != 'v7')):
            raise ValueError('OCI descriptor and image platform disagree')
        info = {'Os': 'linux', 'Architecture': arch, 'Variant': variant, 'Config': config.get('config', {})}
        verify_image(info, self.role, key, self.repository, revision, version)
        rootfs = config.get('rootfs', {})
        if (rootfs.get('type') != 'layers' or len(rootfs.get('diff_ids', [])) != len(document['layers'])
                or not all(re.fullmatch(r'sha256:[0-9a-f]{64}', digest) for digest in rootfs.get('diff_ids', []))):
            raise ValueError('Invalid root filesystem layer inventory')
        for layer in document['layers']:
            if layer['mediaType'] not in ('application/vnd.oci.image.layer.v1.tar', 'application/vnd.oci.image.layer.v1.tar+gzip', 'application/vnd.oci.image.layer.v1.tar+zstd'):
                raise ValueError('Unsupported OCI layer type')
            self.verify_blob(layer)
            self.image_blobs.add(blob_path(layer['digest']))
        self.images[key] = dict(desc, platform=platform)
        self.images[key]['config_digest'] = config_desc['digest']

    def summary(self):
        return {'digest': self.root['digest'], 'platforms': self.images}


def add_bytes(target, name, data):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = 0o644
    target.addfile(info, io.BytesIO(data))


def combine(paths, destination, revision, version, role, repository="fantuz/entropy-service"):
    with ExitStack() as stack:
        sources = [Archive(path, revision, version, role, stack, repository) for path in paths]
        images, blobs = {}, {}
        for source in sources:
            for arch, desc in source.images.items():
                if arch in images:
                    raise ValueError(f'Duplicate platform: {arch}')
                images[arch] = {key: value for key, value in desc.items() if key != 'config_digest'}
            # Original single-platform wrapper indexes are not reachable from
            # the new three-platform root. Copy only actual image payloads.
            for path in source.image_blobs:
                blobs[path] = source
        if set(images) != set(PLATFORMS):
            raise ValueError('All three Linux platforms must pass before publication')
        index = encoded({'schemaVersion': 2, 'mediaType': INDEX,
                         'manifests': [images[arch] for arch in sorted(images)],
                         'annotations': {PREFIX + 'revision': revision, PREFIX + 'version': version,
                                         PREFIX + 'source': 'https://github.com/' + repository}})
        root = descriptor(index, INDEX, annotations={PREFIX + 'ref.name': 'entropy-' + role})
        with tarfile.open(destination, 'w') as target:
            # Skopeo's rootless untar must not have to create implicit parents:
            # containers/storage otherwise tries to chown those parents to root.
            for name in ('blobs/', 'blobs/sha256/'):
                info = tarfile.TarInfo(name)
                info.type, info.mode = tarfile.DIRTYPE, 0o755
                target.addfile(info)
            add_bytes(target, 'oci-layout', encoded({'imageLayoutVersion': '1.0.0'}))
            add_bytes(target, 'index.json', encoded({'schemaVersion': 2, 'manifests': [root]}))
            add_bytes(target, blob_path(root['digest']), index)
            for path, source in sorted(blobs.items()):
                member = source.member(path)
                info = tarfile.TarInfo(path)
                info.size, info.mode = member.size, 0o644
                with source.tar.extractfile(member) as stream:
                    target.addfile(info, stream)
        return root['digest']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--revision', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--role', choices=('server', 'client'), required=True)
    parser.add_argument('--repository', default='fantuz/entropy-service')
    commands = parser.add_subparsers(dest='command', required=True)
    verify = commands.add_parser('verify')
    verify.add_argument('archive', type=Path)
    verify.add_argument('--architectures', default=','.join(PLATFORMS))
    merge = commands.add_parser('combine')
    merge.add_argument('--output', type=Path, required=True)
    merge.add_argument('archives', nargs='+', type=Path)
    args = parser.parse_args()
    if args.command == 'combine':
        print(combine(args.archives, args.output, args.revision, args.version, args.role, args.repository))
    else:
        with ExitStack() as stack:
            archive = Archive(args.archive, args.revision, args.version, args.role, stack, args.repository)
            if set(archive.images) != set(args.architectures.split(',')):
                raise ValueError('Archive does not contain exactly the requested platforms')
            print(json.dumps(archive.summary(), sort_keys=True))


if __name__ == '__main__':
    main()
