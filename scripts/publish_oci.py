"""Publish the exact offline OCI bundles, preserving tested blobs and indexes."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import ipaddress
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

from oci_release import PLATFORMS, native_inputs, verify


def registry_options(registry, local, authfile):
    if local:
        parsed = urlsplit('http://' + registry)
        try:
            valid = ipaddress.ip_address(parsed.hostname or '').is_loopback
        except ValueError:
            valid = False
        if not valid or not parsed.port or parsed.path or parsed.username or parsed.password:
            raise ValueError('Plain HTTP registry testing requires a literal loopback IP and port')
    elif registry != 'ghcr.io':
        raise ValueError('Production publication is restricted to ghcr.io')
    file = Path(authfile) if authfile else Path(os.environ.get('DOCKER_CONFIG', str(Path.home()/'.docker')))/'config.json'
    if not file.is_file():
        raise ValueError('Registry authentication file is missing; log in first or supply --authfile')
    return ['--authfile', str(file)], ['--registry-config', str(file)]


def publish(args, command):
    options, oras_options = registry_options(args.registry, args.local_registry, args.authfile)
    owner = args.repository.lower().split('/', 1)[0]
    destinations = {'server': args.registry+'/'+args.repository.lower(), 'client': args.registry+'/'+owner+'/entropy-client'}
    if destinations['server'] == destinations['client']:
        raise ValueError('Server and client images require distinct destination repositories')
    native = native_inputs(args.artifacts, args.source_sha, args.tag.removeprefix('v'), args.repository)
    folder = args.artifacts/'container-oci-bundles'
    bundles = verify(folder, args.source_sha, args.tag.removeprefix('v'), args.repository, native)
    inspect_options = options + (['--tls-verify=false'] if args.local_registry else [])
    copy_options = options + (['--src-tls-verify=false', '--dest-tls-verify=false'] if args.local_registry else [])
    attach_options = oras_options + (['--plain-http'] if args.local_registry else [])

    def inspect(reference):
        return command(['skopeo', 'inspect', '--raw', *inspect_options, 'docker://'+reference], capture=True)

    def copy(source, destination):
        command(['skopeo', 'copy', '--all', '--format=oci', '--preserve-digests', '--retry-times=0', *copy_options, source, 'docker://'+destination])

    def expect(reference, digest):
        raw = inspect(reference)
        if 'sha256:'+hashlib.sha256(raw).hexdigest() != digest:
            raise ValueError('Registry manifest/index differs from the tested OCI digest')
        return json.loads(raw)

    def stage_image(role, destination):
        image = bundles['images'][role]
        stage = destination+':stage-'+args.tag
        copy('oci-archive:'+str((folder/image['file']).resolve()), stage)
        expect(stage, image['digest'])
        for platform in PLATFORMS:
            leaf = image['platforms'][platform]
            manifest = expect(destination+'@'+leaf['digest'], leaf['digest'])
            if manifest.get('config', {}).get('digest') != leaf['config_digest']:
                raise ValueError('Published config differs from the exact tested OCI image')
            sbom = native[role][platform]['sbom']
            command(['oras', 'attach', *attach_options, '--artifact-type', 'application/spdx+json',
                     destination+'@'+leaf['digest'], sbom.name+':application/spdx+json'], cwd=sbom.parent)

    # The two repositories stage independently. Join both workers and propagate
    # any failure before writing consumer tags, including architecture aliases.
    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(stage_image, role, destination) for role, destination in destinations.items()]
        for future in futures:
            future.result()

    # Preserve existing architecture aliases and SBOM-discovery instructions.
    for role, destination in destinations.items():
        image = bundles['images'][role]
        for platform, leaf in image['platforms'].items():
            reference = destination+':'+args.tag+'-'+platform
            copy('docker://'+destination+'@'+leaf['digest'], reference)
            expect(reference, leaf['digest'])
    references = []
    for role, destination in destinations.items():
        reference = destination+':'+args.tag
        digest = bundles['images'][role]['digest']
        copy(f'docker://{destination}@{digest}', reference)
        expect(reference, digest)
        references.append(reference)
    print('Published exact tested OCI indexes:\n'+'\n'.join(references))
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with Path(os.environ['GITHUB_STEP_SUMMARY']).open('a') as out:
            out.write('### Published offline OCI bundles\n\n'+''.join('- `'+ref+'`\n' for ref in references))
    return bundles
