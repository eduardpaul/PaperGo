#!/usr/bin/env python3
"""Package the verified FP32 Laya bundle reproducibly for offline deployment."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[2]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory',type=Path)
    parser.add_argument('--output',type=Path,default=Path('.tools/decision/laya-fp32.tar.gz'))
    args=parser.parse_args()
    manifest=json.loads((ROOT/'internal/decision/manifest.json').read_bytes())
    if json.loads((args.directory/'manifest.json').read_bytes())!=manifest:
        raise ValueError('Unsupported manifest')
    for name,expected in manifest['files'].items():
        with (args.directory/name).open('rb') as source:
            if hashlib.file_digest(source,'sha256').hexdigest()!=expected:
                raise ValueError(f'Checksum mismatch: {name}')
    args.output.parent.mkdir(parents=True,exist_ok=True)
    partial=args.output.with_suffix('.partial')
    try:
        with partial.open('wb') as target,gzip.GzipFile(filename='',fileobj=target,mode='wb',mtime=0,compresslevel=1) as gz,tarfile.open(fileobj=gz,mode='w|') as archive:
            for name in sorted(['manifest.json',*manifest['files']]):
                path=args.directory/name
                info=tarfile.TarInfo(name)
                info.size=path.stat().st_size
                info.mode=0o644
                with path.open('rb') as source:
                    archive.addfile(info,source)
        partial.replace(args.output)
    finally:
        partial.unlink(missing_ok=True)
    with args.output.open('rb') as source:
        digest=hashlib.file_digest(source,'sha256').hexdigest()
    args.output.with_suffix(args.output.suffix+'.sha256').write_text(f'{digest}  {args.output.name}\n')
    print(f'{digest}  {args.output}')

if __name__=='__main__':
    main()
