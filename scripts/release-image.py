#!/usr/bin/env python3
"""Publish tested OCI digests without replacing an existing release image."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess

spec = importlib.util.spec_from_file_location('release_metadata', Path(__file__).with_name('release-metadata.py'))
metadata = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metadata)
ARCHITECTURES = {'amd64', 'arm64'}


def image_name(repository):
    if not re.fullmatch(r'[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?/[\w.-]+', repository, re.ASCII):
        raise ValueError('Invalid GitHub repository')
    return 'ghcr.io/' + repository.lower()


def package_exists(repository):
    owner, name = repository.lower().split('/')
    owner_type = subprocess.check_output(['gh', 'api', f'repos/{repository}', '--jq', '.owner.type'], text=True).strip()
    scope = 'orgs' if owner_type == 'Organization' else 'users'
    result = subprocess.run(['gh', 'api', f'{scope}/{owner}/packages/container/{name}'], capture_output=True, text=True)
    if result.returncode:
        if '(HTTP 404)' in result.stderr:
            return False
        raise RuntimeError(result.stderr)
    return True


def inspect(image, tag):
    result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', '--raw', f'{image}:{tag}'],
                            capture_output=True, text=True)
    if result.returncode:
        if any(message in result.stderr.lower() for message in ('manifest unknown', 'not found', 'no such manifest')):
            return None
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout)


def validate(manifest, tag, commit):
    annotations = manifest.get('annotations', {})
    if (annotations.get('org.opencontainers.image.version') != tag or
            annotations.get('org.opencontainers.image.revision') != commit):
        raise ValueError('Existing release image has different version/revision metadata; it will not be replaced')
    platforms = {(entry.get('platform', {}).get('os'), entry.get('platform', {}).get('architecture'))
                 for entry in manifest.get('manifests', [])}
    platforms.discard(('unknown', 'unknown'))  # BuildKit provenance/SBOM descriptors.
    if platforms != {('linux', arch) for arch in ARCHITECTURES}:
        raise ValueError('Release image must contain Linux amd64 and arm64')


def publish(image, repository, tag, commit, directory):
    existing = inspect(image, tag)
    if existing is not None:
        validate(existing, tag, commit)
        print(f'{image}:{tag} already exists; preserving it')
        return
    sources = []
    for arch in sorted(ARCHITECTURES):
        digest = (Path(directory) / arch).read_text().strip()
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
            raise ValueError(f'Invalid tested image digest for {arch}')
        sources.append(f'{image}@{digest}')
    args = ['docker', 'buildx', 'imagetools', 'create', '--tag', f'{image}:{tag}']
    for key, value in {'version': tag, 'revision': commit, 'source': f'https://github.com/{repository}'}.items():
        args += ['--annotation', f'index:org.opencontainers.image.{key}={value}']
    subprocess.run([*args, *sources], check=True)
    validate(inspect(image, tag), tag, commit)


def promote_latest(image, repository, tag):
    if '-rc.' in tag:
        return
    latest = json.loads(subprocess.check_output(['gh', 'api', f'repos/{repository}/releases/latest'], text=True))
    if latest['tag_name'] == tag:
        subprocess.run(['docker', 'buildx', 'imagetools', 'create', '--tag', f'{image}:latest', f'{image}:{tag}'], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'publish'])
    parser.add_argument('--digests', type=Path, default=Path('digests'))
    args = parser.parse_args()
    repository, tag, commit = (os.environ[key] for key in ['GITHUB_REPOSITORY', 'RELEASE_TAG', 'RELEASE_COMMIT'])
    metadata.version_key(tag)
    image = image_name(repository)
    if args.command == 'check':
        # GHCR refuses even token negotiation for a package that has never
        # been pushed. Distinguish that first-publication case through its API.
        existing = inspect(image, tag) if package_exists(repository) else None
        if existing is not None:
            validate(existing, tag, commit)
        with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as output:
            output.write(f'image={image}\nstate={"existing" if existing is not None else "missing"}\n')
    else:
        publish(image, repository, tag, commit, args.digests)
        promote_latest(image, repository, tag)


if __name__ == '__main__':
    main()
