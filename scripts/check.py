#!/usr/bin/env python3
"""PaperGo's checks. Each CI job runs one stage; run the same stage locally, or
in the CI toolchain image with scripts/ci.sh.

Stages:
  quick    (default) Go formatting and cached Go tests; the edit/check loop
  lint     formatting, modules, generated code, vet, Staticcheck, the pure-Go
           MCP server, frontend typecheck and lint, Python tests
  test     uncached Go suite on SQLite, database suites on PostgreSQL 17 and
           frontend tests
  race     the Go suite with race detection, but for the packages the native
           and zvec stages race-test with real models and stores
  web      frontend typecheck, lint and tests
  native   real decision, embedding and OCR models
  zvec     native search store conformance and parity
  soak     native search soak at real dimensions and timed fuzzing
  release  build, package and run the release executables
  docker   build and smoke test the container images
  vuln     reachable Go dependency vulnerabilities
"""

import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
STAGES = ['quick', 'lint', 'test', 'race', 'web', 'native', 'zvec', 'soak', 'release', 'docker', 'vuln']
# Suites whose tests open databases through internal/dal/daltest, which run on
# PostgreSQL in the test stage, with the DAL packages postgres_packages adds.
POSTGRES_PACKAGES = ['./internal/dms', './internal/httpapi', './internal/identity', './internal/scriptrun',
                     './internal/webdav', './internal/workflow', './internal/aiops', './internal/worker', './internal/documenttext',
                     './internal/documentpreview', './internal/documentfiles', './internal/documentsearch',
                     './extensions/mcp/integration']
NATIVE_PACKAGES = ['./internal/onnx', './internal/decision', './internal/embedding', './internal/ocr/paddle', './internal/scriptrun',
                   './internal/compose', './internal/optimize', './cmd/embed']
# Packages the zvec stage tests on the native search store.
ZVEC_PACKAGES = ['./internal/dal/search/zvecstore', './internal/backup', './cmd/admin']
# Native libraries the release executables load, exercised before packaging.
NATIVE_ABI = (['./internal/onnx', './internal/ocr/paddle', './internal/imaging', './internal/doctext/pdfium', './internal/dal/search/zvecstore'],
              'TestNativeABI|TestReadAndRender|TestRecognize$|TestDecode|TestWebPRoundTripAndResize|TestIdle|TestConcurrentIdle')
RELEASE_VERSION = 'v0.0.0-rc.1'
# The only PaperGo packages papergo-mcp may import: it reaches PaperGo
# through its HTTP API alone.
MCP_IMPORTS = ['papergo/internal/buildinfo', 'papergo/internal/dal/refs']


def analyzed_packages():
    """Go packages Staticcheck analyzes: generated Bob models are left out,
    as their hook plumbing assigns contexts it does not use."""
    listed = subprocess.run(['go', 'list', './...'], cwd=ROOT, check=True, stdout=subprocess.PIPE, text=True).stdout.split()
    return [package for package in listed if not re.search(r'/internal/dal/driver/[^/]+/models$', package)]


def run(*command, env=None):
    print('+ ' + ' '.join(map(str, command)), flush=True)
    executable = shutil.which(str(command[0])) or str(command[0])
    subprocess.run([executable, *map(str, command[1:])], cwd=ROOT, check=True, env=env)


def digest(paths, root):
    result = hashlib.sha256()
    for path in sorted(paths):
        result.update(Path(os.path.relpath(path, root)).as_posix().encode() + b'\0')
        result.update(path.read_bytes())
        result.update(b'\0')
    return result.hexdigest()


def files(*directories):
    return [path for directory in directories for path in (ROOT / directory).rglob('*') if path.is_file()]


def frontend_state():
    web = ROOT / 'web'
    sources = []
    for directory, folders, names in os.walk(web):
        folders[:] = sorted(name for name in folders if name not in {'node_modules', 'dist', '.git'})
        sources.extend(Path(directory) / name for name in names if not name.endswith('.tsbuildinfo'))
    # The script editor bundles the workflow SDK's declarations.
    sources.append(ROOT / 'sdk/dist/workflow.d.ts')
    outputs = [path for path in (web / 'dist').rglob('*') if path.is_file()]
    return {'source': digest(sources, web), 'output': digest(outputs, web)}


def node_dependencies(project, install=False):
    """Require a project's installed npm dependencies to match its lockfile;
    with install, run npm ci when they do not."""
    directory = ROOT / project
    manifest = json.loads((directory / 'package.json').read_text())
    lock = json.loads((directory / 'package-lock.json').read_text())['packages']
    installed_path = directory / 'node_modules/.package-lock.json'
    problem = None
    if not installed_path.is_file():
        problem = f'{project} dependencies are missing; run npm ci --prefix {project}'
    else:
        installed = json.loads(installed_path.read_text())['packages']
        # npm's hidden lock contains the installed platform subset. Optional
        # packages may be absent, but every installed entry must match the lock.
        for field in ('dependencies', 'devDependencies', 'optionalDependencies'):
            if manifest.get(field, {}) != lock[''].get(field, {}):
                raise ValueError(f'{project}/package.json and package-lock.json differ; update the lockfile')
        stale = any(lock.get(name) != entry or not (directory / name).is_dir() for name, entry in installed.items())
        stale |= any(name and not entry.get('optional') and name not in installed for name, entry in lock.items())
        if stale:
            problem = f'{project} dependencies are stale; run npm ci --prefix {project}'
    if problem and not install:
        raise ValueError(problem)
    if problem:
        run('npm', 'ci', '--prefix', project, '--no-audit', '--no-fund')


def prepare_frontend(install=False, force=False):
    stamp = ROOT / '.cache/checks/frontend.json'
    state = frontend_state()
    if not force and (ROOT / 'web/dist/index.html').is_file() and stamp.is_file():
        if json.loads(stamp.read_text()) == state:
            print('Embedded frontend is current.', flush=True)
            return
    node_dependencies('web', install)
    run('npm', '--prefix', 'web', 'run', 'build')
    stamp.parent.mkdir(parents=True, exist_ok=True)
    # The router plugin can regenerate a source file during the build.
    stamp.write_text(json.dumps(frontend_state()) + '\n')


def native_directory():
    return Path(os.environ.get('PAPERGO_NATIVE_DIR') or ROOT / '.tools/native')


def prepare_go():
    local_go = ROOT / '.tools/go/bin/go.exe'
    if os.name == 'nt' and local_go.is_file():
        os.environ['PATH'] = str(local_go.parent) + os.pathsep + os.environ['PATH']
        for key, directory in {'GOCACHE': 'build', 'GOMODCACHE': 'mod', 'GOPATH': 'gopath'}.items():
            os.environ[key] = str(ROOT / '.cache' / directory)
    native = native_directory()
    run(sys.executable, 'scripts/native.py', '--check', '--directory', native)
    loaders = ('LD_LIBRARY_PATH', 'DYLD_LIBRARY_PATH', 'PATH')
    for line in (native / 'native.env').read_text().splitlines():
        key, value = line.split('=', 1)
        if key not in loaders:
            os.environ[key] = value
    # The library directory goes first on the current search path: the copy
    # in native.env would undo later changes, such as tools added to PATH.
    loader = {'nt': 'PATH'}.get(os.name, 'DYLD_LIBRARY_PATH' if sys.platform == 'darwin' else 'LD_LIBRARY_PATH')
    os.environ[loader] = os.pathsep.join(filter(None, [str(native / 'lib'), os.environ.get(loader)]))
    # The models ship in this checkout, wherever the SDKs were installed.
    os.environ['OCR_MODELS'] = str(ROOT / 'models/paddleocr')


def postgres_packages():
    """The portable database suites. Native search is covered in its own
    stage, not rerun against PostgreSQL."""
    listed = subprocess.run(['go', 'list', './internal/dal/...'], cwd=ROOT, check=True, stdout=subprocess.PIPE, text=True).stdout.split()
    return [package for package in listed if not re.search(r'/internal/dal/search(/|$)', package)] + POSTGRES_PACKAGES


def race_packages():
    """The Go suite but for the packages the native and zvec stages already
    run with race detection."""
    listed = lambda *patterns: subprocess.run(['go', 'list', *patterns], cwd=ROOT, check=True, stdout=subprocess.PIPE, text=True).stdout.split()
    covered = set(listed(*NATIVE_PACKAGES, *ZVEC_PACKAGES))
    return [package for package in listed('./...') if package not in covered]


def prepare_postgres():
    """Point DAL and application tests at a PostgreSQL server: the one the
    environment names, or the disposable server of scripts/postgres.sh."""
    dsn = os.environ.get('PAPERGO_TEST_POSTGRES_DSN') or os.environ.get('TEST_POSTGRES_DSN')
    if not dsn:
        dsn = subprocess.run(['bash', 'scripts/postgres.sh', 'dsn'], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
    os.environ.setdefault('PAPERGO_TEST_POSTGRES_DSN', dsn)
    os.environ.setdefault('TEST_POSTGRES_DSN', dsn)
    print('+ PostgreSQL tests use ' + dsn.split('@')[-1], flush=True)


def prepare_models():
    """Point the native tests at the model presets in .tools, which
    scripts/prepare-preset-models.py installs."""
    for variable, default in (('DECISION_MODELS_PATH', '.tools/decision/model'), ('EMBEDDING_MODELS_ROOT', '.tools/embedding/models')):
        path = Path(os.environ.get(variable) or ROOT / default)
        if not path.is_dir():
            raise ValueError(f'{variable} must point to the installed model presets; run python3 scripts/prepare-preset-models.py')
        os.environ[variable] = str(path)


def check_formatting():
    # Directory traversal also keeps this below Windows' command-line limit.
    paths = [name for name in ('cmd', 'extensions', 'internal') if (ROOT / name).is_dir()]
    paths += [str(path.relative_to(ROOT)) for path in sorted((ROOT / 'web').glob('*.go'))]
    result = subprocess.run(['gofmt', '-l', *paths], cwd=ROOT, check=True, capture_output=True, text=True)
    if result.stdout:
        raise ValueError('Run gofmt on:\n' + result.stdout)


def check_mcp():
    """papergo-mcp builds as pure Go and imports no application package."""
    run('go', 'build', '-o', os.devnull, './cmd/mcp', env=environment(CGO_ENABLED='0'))
    listed = subprocess.run(['go', 'list', '-deps', './cmd/mcp'], cwd=ROOT, check=True, stdout=subprocess.PIPE, text=True,
                            env=environment(CGO_ENABLED='0')).stdout.split()
    imported = sorted(package for package in listed if package.startswith('papergo/internal/'))
    if imported != MCP_IMPORTS:
        raise ValueError('papergo-mcp imports ' + ', '.join(imported) + '; it may only import ' + ', '.join(MCP_IMPORTS))


def check_web(install=False, tests=True):
    node_dependencies('web', install)
    run('npm', '--prefix', 'web', 'run', 'typecheck')
    run('npm', '--prefix', 'web', 'run', 'lint')
    if tests:
        run('npm', '--prefix', 'web', 'test', '--', '--maxWorkers=2')


def unchanged(label, generate, outputs):
    before = digest(outputs(), ROOT)
    generate()
    if before != digest(outputs(), ROOT):
        raise ValueError(f'{label} generation updated files. Review the generated changes, commit them and rerun the checks.')


def check_generated(install=False):
    def models():
        directories = [ROOT / 'internal/dal/driver' / engine / 'models' for engine in ('sqlite', 'postgres')]
        return [ROOT / 'go.mod', ROOT / 'go.sum', *sorted(path for directory in directories for path in directory.rglob('*.go'))]
    prepare_postgres()
    unchanged('Bob', lambda: run('bash', 'scripts/generate-models.sh'), models)

    def sdk():
        node_dependencies('sdk', install)
        for script in ('generate', 'typecheck', 'test', 'build'):
            run('npm', '--prefix', 'sdk', 'run', script)
    unchanged('Workflow SDK', sdk, lambda: files('sdk/src/client', 'sdk/dist'))


def shard_names(listing, shard, shards):
    if shards < 1 or not 1 <= shard <= shards:
        raise ValueError('shard must be between 1 and shards')
    # A name shared by several packages must stay on the same shard because
    # Go's -run expression applies to every package. Subtests run with the parent.
    names = sorted(set(re.findall(r'^(?:Test|Fuzz|Example)\w*$', listing, re.MULTILINE)))
    if not names:
        raise ValueError('Go did not list any tests')
    return names[shard - 1::shards]


def run_pattern(names):
    # An empty pattern would run every test, so empty shards must match nothing.
    return '^(' + '|'.join(re.escape(name) for name in names) + ')$' if names else '^$'


def go_tests(name, flags, packages, shard=None, env=None):
    """Run Go tests with reports in .cache/checks/<name>.*: all of them, or
    one shard of the test, fuzz seed and example names."""
    started = time.monotonic()
    report = ROOT / '.cache/checks' / name
    report.parent.mkdir(parents=True, exist_ok=True)
    if shard:
        try:
            listing = subprocess.run(['go', 'test', *flags, '-list', '^(Test|Fuzz|Example)', *packages], cwd=ROOT, check=True,
                                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=env).stdout
        except subprocess.CalledProcessError as error:
            report.with_suffix('.discovery.log').write_text(error.stdout or '', encoding='utf-8')
            print(error.stdout or '', file=sys.stderr)
            raise
        report.with_suffix('.discovery.log').write_text(listing, encoding='utf-8')
        names = shard_names(listing, *shard)
        # The shard's -run expression is long; the log names the shard instead.
        print(f'+ go test {" ".join(flags)} {" ".join(packages)}: shard {shard[0]}/{shard[1]}, {len(names)} test names', flush=True)
        flags = [*flags, '-run', run_pattern(names)]
    else:
        print(f'+ go test {" ".join(flags)} {" ".join(packages)}', flush=True)
    result = subprocess.run([sys.executable, str(ROOT / 'scripts/go-test.py'), '--output', str(report),
                             '--preparation-seconds', str(time.monotonic() - started), '--', *flags, *packages],
                            cwd=ROOT, check=False, env=env)
    if result.returncode:
        raise subprocess.CalledProcessError(result.returncode, ['go', 'test', *packages])


def environment(**values):
    return {**os.environ, **values}


def sqlite_environment(**values):
    """The environment of tests that run on SQLite, without a PostgreSQL
    server that PAPERGO_TEST_POSTGRES_DSN or TEST_POSTGRES_DSN would name."""
    return {key: value for key, value in environment(**values).items()
            if key not in ('PAPERGO_TEST_POSTGRES_DSN', 'TEST_POSTGRES_DSN')}


def stage_lint(args):
    prepare_go()
    check_formatting()
    run('go', 'mod', 'verify')
    check_generated(args.setup)
    prepare_frontend(args.setup)
    run('go', 'vet', './...')
    if not shutil.which('staticcheck'):
        raise ValueError('Staticcheck is required; it is in the toolchain image (scripts/ci.sh lint)')
    run('staticcheck', *analyzed_packages())
    check_mcp()
    check_web(args.setup, tests=False)
    for directory in ('scripts/tests', 'samples/npm', 'samples/authentik'):
        run(sys.executable, '-m', 'unittest', 'discover', '-s', directory, '-p', 'test_*.py')


def stage_test(args):
    parts = [args.only] if args.only else ['go', 'postgres', 'web']
    if 'web' in parts:
        node_dependencies('web', args.setup)
        run('npm', '--prefix', 'web', 'test', '--', '--maxWorkers=2')
    if 'go' in parts or 'postgres' in parts:
        prepare_go()
        prepare_frontend(args.setup)
    if 'go' in parts:
        go_tests('test' + (f'-{args.shard[0]}' if args.shard else ''), ['-count=1', '-timeout=15m', *run_flags(args)],
                 args.packages or ['./...'], args.shard, sqlite_environment(PAPERGO_REQUIRE_OCR='1'))
    if 'postgres' in parts:
        prepare_postgres()
        go_tests('postgres', ['-count=1', '-timeout=40m', *run_flags(args)], args.packages or postgres_packages())


def stage_race(args):
    prepare_go()
    prepare_frontend(args.setup)
    go_tests('race' + (f'-{args.shard[0]}' if args.shard else ''), ['-race', '-count=1', '-timeout=15m', *run_flags(args)],
             args.packages or race_packages(), args.shard, sqlite_environment())


def stage_native(args):
    prepare_models()
    prepare_go()
    prepare_frontend(args.setup)
    go_tests('native', ['-race', '-count=1', '-timeout=15m', *run_flags(args)], args.packages or NATIVE_PACKAGES,
             env=sqlite_environment(PAPERGO_REQUIRE_DECISION='1', PAPERGO_REQUIRE_EMBEDDING='1', PAPERGO_REQUIRE_OCR='1'))


def stage_zvec(args):
    prepare_go()
    prepare_frontend(args.setup)
    go_tests('zvec', ['-race', '-count=1'], ZVEC_PACKAGES, env=sqlite_environment())
    go_tests('zvec-parity', ['-race', '-count=1', '-run', '(Search|Content|Receipt|Contract|AIComplete|Prefix|SmartFolder|View|Listing|Query)'],
             ['./internal/dms', './internal/httpapi', './internal/scriptrun'], env=sqlite_environment(PAPERGO_TEST_SEARCH_STORE='zvec'))


def stage_soak(args):
    prepare_go()
    prepare_frontend(args.setup)
    go_tests('zvec-soak', ['-count=1', '-run', '^TestRealDimensionsSoak$', '-v'], ['./internal/dal/search/zvecstore'],
             env=sqlite_environment(PAPERGO_ZVEC_SOAK='1'))
    for target in ('FuzzNativeFilter', 'FuzzNativeText'):
        run('go', 'test', './internal/dal/search/zvecstore', '-run', '^$', '-fuzz', f'^{target}$', '-fuzztime=10s', '-parallel=2')


def stage_release(args):
    """Rehearse a release as the release workflow does: build, package and
    run the executables."""
    prepare_models()
    prepare_go()
    prepare_frontend(args.setup, force=True)
    go_tests('native-abi', ['-count=1', '-run', NATIVE_ABI[1]], NATIVE_ABI[0], env=sqlite_environment())
    # Extracted packages and assets take several GiB; keep them off a small /tmp.
    temporary = ROOT / '.cache/tmp'
    temporary.mkdir(parents=True, exist_ok=True)
    os.environ.setdefault('TMPDIR', str(temporary))
    goos, goarch = (subprocess.run(['go', 'env', name], check=True, capture_output=True, text=True).stdout.strip() for name in ('GOOS', 'GOARCH'))
    assets = f'dist/papergo_{RELEASE_VERSION}_assets.zip'
    run(sys.executable, 'scripts/build-binaries.py', f'--ldflags=-s -w -X papergo/internal/buildinfo.Version={RELEASE_VERSION}')
    run(sys.executable, 'scripts/build-assets.py', '--archive', assets)
    for stale in (ROOT / 'dist').glob(f'papergo_*_{goos}_{goarch}.zip'):
        stale.unlink()
    run(sys.executable, 'scripts/release-package.py', '--version', RELEASE_VERSION, '--os', goos, '--arch', goarch)
    run(sys.executable, 'scripts/release-smoke.py', '--assets', assets)


def stage_docker(args):
    prepare_models()
    if not shutil.which('docker'):
        raise ValueError('The docker stage needs Docker; scripts/ci.sh runs it on the host')
    assets = ROOT / '.cache/container-assets'
    shutil.rmtree(assets, ignore_errors=True)
    run(sys.executable, 'scripts/build-assets.py', '--output-dir', assets / 'assets')
    run('docker', 'build', '--build-context', f'model-assets={assets}', '--tag', 'papergo:check', '.')
    run(sys.executable, 'scripts/docker-smoke.py', '--image', 'papergo:check')
    run('docker', 'build', '--file', 'extensions/mcp/Dockerfile', '--tag', 'papergo-mcp:check', '.')
    run('docker', 'run', '--rm', 'papergo-mcp:check', '--version')


def stage_vuln(args):
    prepare_go()
    prepare_frontend(args.setup)
    run('govulncheck', './...')


def stage_web(args):
    check_web(args.setup)


def stage_quick(args):
    prepare_go()
    prepare_frontend(args.setup)
    check_formatting()
    go_tests('quick', ['-timeout=15m', *run_flags(args)], args.packages or ['./...'])


def run_flags(args):
    return ['-run', args.run] if args.run else []


def parse_shard(value):
    match = re.fullmatch(r'(\d+)/(\d+)', value)
    if not match or not 1 <= int(match[1]) <= int(match[2]):
        raise argparse.ArgumentTypeError('use I/N with 1 <= I <= N, such as 1/2')
    return int(match[1]), int(match[2])


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('stage', nargs='?', default='quick', choices=STAGES)
    parser.add_argument('--packages', nargs='+', metavar='PACKAGE', help='Go packages to test (quick, test, race, native)')
    parser.add_argument('--run', metavar='REGEXP', help='Go test name filter, with --packages')
    parser.add_argument('--only', choices=['go', 'postgres', 'web'], help='one part of the test stage')
    parser.add_argument('--shard', type=parse_shard, metavar='I/N', help='one of N parts of the Go suite (test --only go, race)')
    parser.add_argument('--setup', action='store_true', help='install missing or stale npm dependencies first, as CI does')
    args = parser.parse_args(argv)
    if args.run is not None and not args.packages:
        parser.error('--run requires --packages')
    if args.packages and args.stage not in ('quick', 'test', 'race', 'native'):
        parser.error('--packages applies to quick, test, race and native')
    if args.only and args.stage != 'test':
        parser.error('--only applies to the test stage')
    if args.shard and not (args.stage == 'race' or args.stage == 'test' and args.only == 'go'):
        parser.error('--shard applies to race and test --only go')
    if args.shard and args.packages:
        parser.error('--shard splits the whole suite; it does not combine with --packages')
    started = time.monotonic()
    os.chdir(ROOT)
    globals()['stage_' + args.stage](args)
    print(f'{args.stage} checks passed in {time.monotonic() - started:.2f}s.', flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'Check failed: {error}', file=sys.stderr)
        sys.exit(error.returncode if isinstance(error, subprocess.CalledProcessError) else 1)
