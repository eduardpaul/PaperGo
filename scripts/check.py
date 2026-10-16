#!/usr/bin/env python3
"""Cached development checks; use --full for all validation and a production build."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent


def run(*command):
    print('+ ' + ' '.join(map(str, command)), flush=True)
    executable = shutil.which(str(command[0])) or str(command[0])
    subprocess.run([executable, *map(str, command[1:])], cwd=ROOT, check=True)


def digest(paths, root):
    result = hashlib.sha256()
    for path in sorted(paths):
        result.update(path.relative_to(root).as_posix().encode() + b'\0')
        result.update(path.read_bytes())
        result.update(b'\0')
    return result.hexdigest()


def frontend_state():
    web = ROOT / 'web'
    sources = []
    for directory, folders, files in os.walk(web):
        folders[:] = sorted(name for name in folders if name not in {'node_modules', 'dist', '.git'})
        sources.extend(Path(directory) / name for name in files if not name.endswith('.tsbuildinfo'))
    outputs = [path for path in (web / 'dist').rglob('*') if path.is_file()]
    return {'source': digest(sources, web), 'output': digest(outputs, web)}


def require_web_dependencies():
    web = ROOT / 'web'
    manifest = json.loads((web / 'package.json').read_text())
    lock = json.loads((web / 'package-lock.json').read_text())['packages']
    installed_path = web / 'node_modules/.package-lock.json'
    if not installed_path.is_file():
        raise ValueError('Frontend dependencies are missing; run npm ci --prefix web')
    installed = json.loads(installed_path.read_text())['packages']
    # npm's hidden lock contains the installed platform subset. Optional
    # packages may be absent, but every installed entry must match the lock.
    for field in ('dependencies', 'devDependencies', 'optionalDependencies'):
        if manifest.get(field, {}) != lock[''].get(field, {}):
            raise ValueError('web/package.json and package-lock.json differ; update the lockfile')
    stale = any(lock.get(name) != entry or not (web / name).is_dir() for name, entry in installed.items())
    stale |= any(name and not entry.get('optional') and name not in installed for name, entry in lock.items())
    if stale:
        raise ValueError('Frontend dependencies are stale; run npm ci --prefix web')


def prepare_frontend(force=False):
    stamp = ROOT / '.cache/checks/frontend.json'
    state = frontend_state()
    if not force and (ROOT / 'web/dist/index.html').is_file() and stamp.is_file():
        if json.loads(stamp.read_text()) == state:
            print('Embedded frontend is current.', flush=True)
            return
    require_web_dependencies()
    run('npm', '--prefix', 'web', 'run', 'build')
    stamp.parent.mkdir(parents=True, exist_ok=True)
    # The router plugin can regenerate a source file during the build.
    stamp.write_text(json.dumps(frontend_state()) + '\n')


def prepare_go():
    local_go = ROOT / '.tools/go/bin/go.exe'
    if os.name == 'nt' and local_go.is_file():
        os.environ['PATH'] = str(local_go.parent) + os.pathsep + os.environ['PATH']
        for key, directory in {'GOCACHE': 'build', 'GOMODCACHE': 'mod', 'GOPATH': 'gopath'}.items():
            os.environ[key] = str(ROOT / '.cache' / directory)
    run(sys.executable, 'scripts/native.py', '--check')
    for line in (ROOT / '.tools/native/native.env').read_text().splitlines():
        key, value = line.split('=', 1)
        os.environ[key] = value


def check_formatting():
    # Directory traversal also keeps this below Windows' command-line limit.
    paths = ['cmd', 'internal', 'ent/schema', *[str(path.relative_to(ROOT)) for path in sorted((ROOT / 'web').glob('*.go'))]]
    result = subprocess.run(['gofmt', '-l', *paths], cwd=ROOT, check=True, capture_output=True, text=True)
    if result.stdout:
        raise ValueError('Run gofmt on:\n' + result.stdout)


def check_web(typecheck=True):
    require_web_dependencies()
    if typecheck:
        run('npm', '--prefix', 'web', 'run', 'typecheck')
    run('npm', '--prefix', 'web', 'run', 'lint')
    run('npm', '--prefix', 'web', 'test', '--', '--maxWorkers=2')


def check_generated():
    paths = [ROOT / 'go.mod', ROOT / 'go.sum', *sorted((ROOT / 'ent').rglob('*.go'))]
    before = digest(paths, ROOT)
    run('go', 'generate', './ent')
    paths = [ROOT / 'go.mod', ROOT / 'go.sum', *sorted((ROOT / 'ent').rglob('*.go'))]
    if before != digest(paths, ROOT):
        raise ValueError('Ent generation updated files. Review the generated changes and rerun the checks.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument('--packages', nargs='+', metavar='PACKAGE', help='test explicitly selected Go packages (no automatic dependency selection)')
    modes.add_argument('--web', action='store_true', help='frontend typecheck, lint and tests only')
    modes.add_argument('--full', action='store_true', help='all validation, uncached Go tests and production build')
    modes.add_argument('--race', action='store_true', help='entire Go suite with race detection, uncached')
    modes.add_argument('--native', action='store_true', help='require real decision, embedding and OCR assets and test their integrations')
    parser.add_argument('--run', metavar='REGEXP', help='Go test name filter (only with --packages)')
    args = parser.parse_args()
    if args.run is not None and not args.packages:
        parser.error('--run requires --packages')
    started = time.monotonic()
    os.chdir(ROOT)
    if args.full and not shutil.which('staticcheck'):
        raise ValueError('Staticcheck is required for --full; use the devcontainer or install the pinned tool from CI')
    if args.native:
        for variable in ('DECISION_MODELS_PATH', 'EMBEDDING_MODELS_ROOT'):
            if not os.environ.get(variable) or not Path(os.environ[variable]).is_dir():
                raise ValueError(f'{variable} must point to the installed native model assets')
    if args.web:
        check_web()
    else:
        prepare_go()
        prepare_frontend(force=args.full)
        check_formatting()
        packages = args.packages or ['./...']
        flags = ['-timeout=15m']
        if args.full:
            check_web(typecheck=False)  # The production build already typechecked.
            for directory in ('scripts/tests', 'samples/npm', 'samples/authentik'):
                run(sys.executable, '-m', 'unittest', 'discover', '-s', directory, '-p', 'test_*.py', '-v')
            run('go', 'mod', 'verify')
            check_generated()
            run('go', 'vet', './...')
            run('staticcheck', './...')
            flags += ['-count=1', '-vet=off']
        if args.race or args.native:
            flags += ['-race', '-count=1']
        if args.native:
            os.environ.update(PAPERGO_REQUIRE_DECISION='1', PAPERGO_REQUIRE_EMBEDDING='1', PAPERGO_REQUIRE_OCR='1')
            packages = ['./internal/onnx', './internal/decision', './internal/embedding', './internal/ocr/paddle', './internal/runner']
        if args.run is not None:
            flags += ['-run', args.run]
        run(sys.executable, 'scripts/go-test.py', '--output', '.cache/checks/go-tests', '--', *flags, *packages)
        if args.full:
            (ROOT / 'bin').mkdir(exist_ok=True)
            run('go', 'build', '-trimpath', '-o', 'bin/papergo.exe' if os.name == 'nt' else 'bin/papergo', './cmd/api')
    print(f'Checks passed in {time.monotonic() - started:.2f}s.', flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'Check failed: {error}', file=sys.stderr)
        sys.exit(error.returncode if isinstance(error, subprocess.CalledProcessError) else 1)
