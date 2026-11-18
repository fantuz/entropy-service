#!/usr/bin/env python3
"""Build/verify the two offline OCI downloads from the same tested image inputs."""
import argparse
from contextlib import ExitStack
import hashlib
import json
from pathlib import Path

from check_release_downloads import read_checksums
from container_identity import PLATFORMS
from container_security import verify_security_reports
from oci_bundle import Archive, combine

ROLES = {'server': 'entropy-service', 'client': 'entropy-client'}


def regular(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
        raise ValueError('Expected a nonempty regular artifact: ' + str(path))


def verify_checksums(directory, names):
    regular(directory/'checksums.txt')
    checksums = read_checksums(directory/'checksums.txt')
    if set(checksums) != set(names):
        raise ValueError('OCI checksum inventory differs from the expected assets')
    for name in names:
        path = directory/name
        regular(path)
        with path.open('rb') as source:
            digest = hashlib.file_digest(source, 'sha256').hexdigest()
        if checksums[name] != digest:
            raise ValueError('OCI archive checksum mismatch: ' + name)


def write_checksums(directory, names):
    lines = []
    for name in names:
        with (directory/name).open('rb') as source:
            lines.append(hashlib.file_digest(source, 'sha256').hexdigest() + '  ' + name + '\n')
    (directory/'checksums.txt').write_text(''.join(lines))


def native_inputs(directory, revision, version, repository):
    result = {role: {} for role in ROLES}
    with ExitStack() as stack:
        for platform in PLATFORMS:
            folder = directory/('container-images-' + platform)
            verify_checksums(folder, [role+'.oci.tar' for role in ROLES])
            for name in ('security-checksums.txt', 'server.spdx.json', 'client.spdx.json',
                         'server.vulnerabilities.json', 'client.vulnerabilities.json'):
                regular(folder/name)
            for role in ROLES:
                path = folder/(role+'.oci.tar')
                archive = Archive(path, revision, version, role, stack, repository)
                if set(archive.images) != {platform}:
                    raise ValueError('Native OCI artifact has unexpected platforms')
                image = archive.images[platform]
                sbom = verify_security_reports(folder, role, image['config_digest'])
                result[role][platform] = {'archive': path, 'image': image, 'sbom': sbom}
    return result


def names_for(version):
    return {role: f'{name}_{version}_linux.oci.tar' for role, name in ROLES.items()}


def build(directory, output, revision, version, repository):
    native = native_inputs(directory, revision, version, repository)
    output.mkdir(parents=True, exist_ok=True)
    if list(output.iterdir()):
        raise ValueError('OCI output directory must be empty')
    names = names_for(version)
    metadata = {'source_sha': revision, 'version': version, 'repository': repository, 'images': {}}
    for role, name in names.items():
        digest = combine([native[role][p]['archive'] for p in PLATFORMS], output/name,
                         revision, version, role, repository)
        with ExitStack() as stack:
            archive = Archive(output/name, revision, version, role, stack, repository)
            if archive.root['digest'] != digest:
                raise ValueError('Combined OCI index digest changed')
            metadata['images'][role] = {'file': name, **archive.summary()}
    (output/'oci-images.json').write_text(json.dumps(metadata, indent=2)+'\n')
    write_checksums(output, names.values())
    return verify(output, revision, version, repository)


def verify(directory, revision, version, repository='fantuz/entropy-service', native=None):
    names = names_for(version)
    if {p.name for p in directory.iterdir()} != {*names.values(), 'checksums.txt', 'oci-images.json'}:
        raise ValueError('Unexpected or missing OCI release artifacts')
    verify_checksums(directory, names.values())
    regular(directory/'oci-images.json')
    if (directory/'oci-images.json').stat().st_size > 2*1024*1024:
        raise ValueError('OCI release metadata is too large')
    metadata = json.loads((directory/'oci-images.json').read_text())
    if (metadata.get('version'), metadata.get('source_sha'), metadata.get('repository')) != (version, revision, repository):
        raise ValueError('OCI metadata version/source/repository mismatch')
    if set(metadata.get('images', {})) != set(ROLES):
        raise ValueError('OCI metadata is missing an image role')
    for role, name in names.items():
        with ExitStack() as stack:
            archive = Archive(directory/name, revision, version, role, stack, repository)
            if set(archive.images) != set(PLATFORMS):
                raise ValueError('OCI download must contain all three Linux platforms')
            if metadata['images'][role] != {'file': name, **archive.summary()}:
                raise ValueError('OCI metadata differs from the validated archive')
            if native is not None:
                for platform, image in archive.images.items():
                    expected = native[role][platform]['image']
                    if (image['digest'], image['config_digest']) != (expected['digest'], expected['config_digest']):
                        raise ValueError('OCI bundle differs from exact tested native manifests/configs')
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('build', 'verify'))
    parser.add_argument('--artifacts', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--repository', default='fantuz/entropy-service')
    args = parser.parse_args()
    if args.command == 'build':
        if args.artifacts is None: parser.error('build requires --artifacts')
        result = build(args.artifacts, args.output, args.source_sha, args.version, args.repository)
    else:
        result = verify(args.output, args.source_sha, args.version, args.repository)
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
