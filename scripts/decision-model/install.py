#!/usr/bin/env python3
"""Verify and install a local offline Laya bundle (no inference-time downloads)."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import tarfile
import os
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle', help='verified local directory/archive or HTTPS bundle URL')
    parser.add_argument('--destination', type=Path, default=Path('.tools/decision/model'))
    args = parser.parse_args()
    manifest_bytes = (ROOT / 'internal/decision/manifest.json').read_bytes()
    manifest = json.loads(manifest_bytes)
    with tempfile.TemporaryDirectory() as unpack:
        source=args.bundle
        contract=json.loads(Path(__file__).with_name('bundle.json').read_bytes())
        if '://' in source:
            url=urllib.parse.urlsplit(source)
            if url.scheme!='https' or not url.hostname or url.username or url.password or url.fragment:
                raise ValueError('Bundle downloads require an HTTPS URL without credentials or fragments')
            cache=args.destination.parent/'downloads'
            cache.mkdir(parents=True,exist_ok=True)
            cached=cache/contract['file']
            def valid_archive(path):
                if not path.is_file() or path.stat().st_size!=contract['bytes']:
                    return False
                with path.open('rb') as stream:
                    return hashlib.file_digest(stream,'sha256').hexdigest()==contract['sha256']
            if not valid_archive(cached):
                partial=cached.with_name(f'{cached.name}.{os.getpid()}.partial')
                try:
                    with urllib.request.urlopen(source,timeout=120) as response,partial.open('wb') as target:
                        remaining=contract['bytes']+1
                        while remaining:
                            data=response.read(min(1024*1024,remaining))
                            if not data:
                                break
                            target.write(data)
                            remaining-=len(data)
                    if not valid_archive(partial):
                        raise ValueError('Downloaded bundle checksum/size mismatch')
                    partial.replace(cached)
                finally:
                    partial.unlink(missing_ok=True)
            source=cached
        bundle=Path(source)
        archive_path=bundle
        if bundle.is_file():
            archive_contract=json.loads(Path(__file__).with_name('bundle.json').read_bytes())
            if bundle.stat().st_size!=archive_contract['bytes']:
                raise ValueError('Unsupported archive size')
            with bundle.open('rb') as source:
                if hashlib.file_digest(source,'sha256').hexdigest()!=archive_contract['sha256']:
                    raise ValueError('Archive checksum mismatch')
            bundle=Path(unpack)
            allowed={'manifest.json',*manifest['files']}
            with tarfile.open(archive_path,'r:gz') as archive:
                seen=set()
                for member in archive:
                    if member.name not in allowed or member.name in seen or not member.isfile():
                        raise ValueError(f'Unexpected bundle entry: {member.name}')
                    seen.add(member.name)
                    # Bounds include the pinned graph and tokenizer sizes.
                    if member.size>1_300_000_000:
                        raise ValueError(f'Oversized bundle entry: {member.name}')
                    with archive.extractfile(member) as source,(bundle/member.name).open('wb') as target:
                        shutil.copyfileobj(source,target,1024*1024)
                if seen!=allowed:
                    raise ValueError('Incomplete bundle')
        if json.loads((bundle/'manifest.json').read_bytes())!=manifest:
            raise ValueError('Unsupported bundle manifest')
        # Verify cached installations as well as newly supplied bundles.
        for name,expected in manifest['files'].items():
            with (bundle/name).open('rb') as source:
                if hashlib.file_digest(source,'sha256').hexdigest()!=expected:
                    raise ValueError(f'Checksum mismatch: {name}')
        args.destination.parent.mkdir(parents=True,exist_ok=True)
        with tempfile.TemporaryDirectory(dir=args.destination.parent) as temp:
            staged=Path(temp)
            for name in manifest['files']:
                shutil.copyfile(bundle/name,staged/name)
            (staged/'manifest.json').write_bytes(manifest_bytes)
            args.destination.mkdir(exist_ok=True)
            for source in staged.iterdir():
                source.replace(args.destination/source.name)
    print(args.destination.resolve())

if __name__ == '__main__':
    main()
