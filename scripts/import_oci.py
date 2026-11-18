#!/usr/bin/env python3
"""Load one verified OCI export for runtime testing and scan the same config ID."""
import argparse
from contextlib import ExitStack
import json
from pathlib import Path
import subprocess

from oci_bundle import Archive
from container_identity import PLATFORMS


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--role', choices=('server', 'client'), required=True)
    parser.add_argument('--platform', required=True)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--repository', required=True)
    args = parser.parse_args()
    with ExitStack() as stack:
        archive = Archive(args.archive, args.source_sha, args.version, args.role, stack, args.repository)
        if set(archive.images) != {args.platform}:
            raise ValueError('Expected exactly the requested platform')
        image_id = archive.images[args.platform]['config_digest']
    target = f'entropy-service/{args.role}:{args.platform}'
    arch, variant = PLATFORMS[args.platform]
    selection = ['--override-os=linux', '--override-arch='+arch]
    if variant: selection.append('--override-variant='+variant)
    subprocess.run(['skopeo', *selection, 'copy', 'oci-archive:'+str(args.archive.resolve()), 'docker-daemon:'+target], check=True)
    info = json.loads(subprocess.check_output(['docker', 'image', 'inspect', target]))[0]
    if info['Id'] != image_id:
        raise ValueError('Runtime image config differs from authoritative OCI export')
    print('Imported exact tested OCI config '+image_id)


if __name__ == '__main__':
    main()
