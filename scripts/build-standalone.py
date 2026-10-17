#!/usr/bin/env python3
"""Build one executable containing the application, native runtime and every model preset."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
LIBRARIES = {
    'linux': ['libonnxruntime.so', 'libpdfium.so', 'libzvec_c_api.so'],
    'darwin': ['libonnxruntime.dylib', 'libpdfium.dylib', 'libzvec_c_api.dylib'],
    'windows': ['onnxruntime.dll', 'pdfium.dll', 'zvec_c_api.dll'],
}


def compiler_runtime(system, env):
    """Include the compiler runtimes that are not supplied by the target OS."""
    if system == 'darwin':
        return {}
    compiler = shlex.split(env.get('CXX', 'g++'), posix=system != 'windows')
    # CI supplies Windows compiler paths containing spaces without shell quotes.
    if Path(env.get('CXX', '')).is_file():
        compiler = [env['CXX']]
    names = ['libstdc++.so.6', 'libgcc_s.so.1'] if system == 'linux' else [
        'libstdc++-6.dll', 'libgcc_s_seh-1.dll', 'libwinpthread-1.dll', 'libgomp-1.dll',
    ]
    files = {}
    for name in names:
        located = subprocess.check_output([*compiler, '-print-file-name=' + name], env=env, text=True).strip()
        path = Path(located)
        if not path.is_file():
            executable = shutil.which(compiler[0], path=env.get('PATH'))
            path = Path(executable).parent / name if executable else path
        if path.is_file():
            files[('' if system == 'windows' else 'lib/') + name] = path
        elif system == 'linux':
            raise ValueError(f'Missing compiler runtime {name}')
    # Debian/Ubuntu's compiler copyright files include the runtime exception
    # and redistribution notices. MSYS2 supplies these in share/licenses.
    for package in ('libstdc++6', 'libgcc-s1'):
        notice = Path('/usr/share/doc') / package / 'copyright'
        if notice.is_file():
            files[f'notices/compiler/{package}-copyright'] = notice
    executable = shutil.which(compiler[0], path=env.get('PATH'))
    if executable:
        licenses = Path(executable).parent.parent / 'share/licenses/gcc-libs'
        if licenses.is_dir():
            for path in sorted(licenses.rglob('*')):
                if path.is_file():
                    files['notices/compiler/' + path.relative_to(licenses).as_posix()] = path
    return files


def bundle(path, files, program):
    manifest = {'program': program, 'files': {}}
    with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for name, source in sorted(files.items()):
            mode = 0o700 if name == program or name.startswith('lib/') else 0o600
            info = zipfile.ZipInfo(name)
            info.create_system = 3
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            # Model graphs can exceed a GiB. Stream them instead of allocating
            # a second copy in memory; force ZIP64 for large model files.
            digest, size = hashlib.sha256(), 0
            with source.open('rb') as stream, archive.open(info, 'w', force_zip64=True) as target:
                while block := stream.read(1024 * 1024):
                    digest.update(block)
                    size += len(block)
                    target.write(block)
            manifest['files'][name] = {'sha256': digest.hexdigest(), 'size': size, 'mode': mode}
        archive.writestr('runtime.json', json.dumps(manifest, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--command', choices=['api', 'admin'], default='api')
    parser.add_argument('--output', type=Path)
    parser.add_argument('--native-dir', type=Path, default=ROOT / '.tools/native')
    parser.add_argument('--ldflags', default='-s -w')
    args = parser.parse_args()
    system = platform.system().lower()
    suffix = '.exe' if system == 'windows' else ''
    output = (args.output or ROOT / 'bin' / (('papergo' if args.command == 'api' else 'papergo-admin') + suffix)).resolve()
    native = args.native_dir.resolve()
    env = dict(os.environ)
    for line in (native / 'native.env').read_text().splitlines():
        key, value = line.split('=', 1)
        env[key] = value
    files = {('' if system == 'windows' else 'lib/') + name: native / 'lib' / name for name in LIBRARIES[system]}
    files.update(compiler_runtime(system, env))
    for path in sorted((native / 'notices').rglob('*')):
        if path.is_file():
            files['notices/' + path.relative_to(native / 'notices').as_posix()] = path
    for name in ('LICENSE', 'NOTICE'):
        files['notices/decision/' + name] = ROOT / 'internal/decision' / name
    if args.command == 'api':
        subprocess.run([sys.executable, str(ROOT / 'scripts/prepare-preset-models.py')], cwd=ROOT, check=True)
        for path in sorted((ROOT / 'models/paddleocr').iterdir()):
            if path.is_file():
                files['models/paddleocr/' + path.name] = path
        decision = json.loads((ROOT / 'internal/decision/manifest.json').read_bytes())
        for name in ['manifest.json', *decision['files']]:
            files['models/laya/' + name] = ROOT / '.tools/decision/model' / name
        spec = importlib.util.spec_from_file_location('presets', ROOT / 'scripts/fetch-embedding-models.py')
        presets = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(presets)
        for model, preset in presets.MODELS.items():
            for name in preset['files']:
                files[f'models/embedding/{model}/{name}'] = ROOT / '.tools/embedding/models' / model / name
        for path in sorted((ROOT / 'scripts/model-notices').iterdir()):
            files['notices/models/' + path.name] = path
    (ROOT / '.cache').mkdir(exist_ok=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    # Build inside the module so the launcher can import the internal runtime.
    with tempfile.TemporaryDirectory(prefix='standalone-', dir=ROOT / '.cache') as temporary:
        staging = Path(temporary)
        program = 'papergo-core' + suffix
        subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + args.ldflags, '-o', str(staging / program), './cmd/' + args.command], cwd=ROOT, env=env, check=True)
        files[program] = staging / program
        bundle(staging / 'payload.zip', files, program)
        (staging / 'main.go').write_text('''package main

import (
    _ "embed"
    "papergo/internal/standalone"
)

//go:embed payload.zip
var payload []byte

func main() { standalone.Main(payload, SERVER) }
'''.replace('SERVER', 'true' if args.command == 'api' else 'false'))
        launcher_env = dict(env, CGO_ENABLED='0')
        target = staging / ('papergo' + suffix)
        subprocess.run(['go', 'build', '-trimpath', '-ldflags=' + args.ldflags, '-o', str(target), str(staging)], cwd=ROOT, env=launcher_env, check=True)
        # os.replace is atomic and also replaces an existing Windows executable
        # once its process has exited.
        shutil.copy2(target, output.with_name(output.name + '.new'))
        os.replace(output.with_name(output.name + '.new'), output)
    print(f'Self-contained executable: {output}')


if __name__ == '__main__':
    main()
