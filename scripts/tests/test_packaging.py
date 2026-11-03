import hashlib
import importlib.util
import json
import re
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parents[1] / filename)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


ROOT = Path(__file__).parents[2]
VC_NOTICE = ROOT / 'scripts/native-notices/microsoft-visual-cpp-runtime.txt'
build = module('binaries', 'build-binaries.py')
assets = module('assets', 'build-assets.py')
package = module('package', 'release-package.py')
prepare = module('presets', 'prepare-preset-models.py')
metadata = module('metadata', 'release-metadata.py')


class PackagingTests(unittest.TestCase):
    def test_platform_and_shared_zips_form_one_installation(self):
        for system, arch in [('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64')]:
            with self.subTest(system=system, arch=arch), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                binaries = root / 'bin'
                native = root / 'native'
                for library in build.LIBRARIES[system]:
                    path = native / 'lib' / library
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(b'native library')
                notice = native / 'notices/dependency/LICENSE'
                notice.parent.mkdir(parents=True)
                notice.write_text('native license')
                compiler = {}
                if system == 'windows':
                    # native.py installs the ONNX Runtime and Visual C++ notices;
                    # MSYS2 supplies the statically linked compiler runtimes' licences.
                    for name in package.WINDOWS_NOTICES:
                        if name.startswith('notices/compiler/'):
                            compiler[name] = notice
                        elif name == package.VC_RUNTIME_NOTICE:
                            (native / name).write_text(VC_NOTICE.read_text())
                        else:
                            (native / name).write_text('notice')
                # The web and SDK builds write their npm notices.
                npm = {name: root / name.replace('/', '-') for name in build.NPM_NOTICES}
                for path in npm.values():
                    path.write_text('npm notices')
                with patch.object(build, 'windows_compiler_notices', return_value=compiler) as windows, \
                        patch.object(build, 'NPM_NOTICES', npm):
                    files = build.runtime_files(native, system, {})
                self.assertEqual(windows.called, system == 'windows')
                # Linux uses the distribution's C++ runtime.
                self.assertEqual([name for name in files if name.startswith('lib/')], ['lib/' + name for name in build.LIBRARIES[system]])
                self.assertEqual(any(name.startswith('notices/compiler/') for name in files), system == 'windows')
                suffix = '.exe' if system == 'windows' else ''
                path = binaries / ('papergo' + suffix)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'application')
                path.chmod(0o755)
                assets.collect(binaries, files)
                # build-binaries.py generates the Go notices for the platform it builds.
                go_notices = binaries / build.GO_NOTICES
                go_notices.parent.mkdir(parents=True)
                go_notices.write_text(f'Third-party notices for the Go code linked into papergo ({system}/{arch})\n')
                model = root / 'model.onnx'
                data = bytes(range(256)) * 8193
                model.write_bytes(data)
                models = {f'models/{name}/model.onnx': model for name in [
                    'paddleocr', 'laya', 'embedding/embeddinggemma-2', 'embedding/harrier-oss-v1-0.6b']}
                models['notices/models.txt'] = notice
                assets.collect(binaries / 'assets', models)
                shared = root / 'papergo_v0.2.0_assets.zip'
                assets.archive_assets(shared, binaries / 'assets', models)
                # The placeholder files are not ELF; the glibc check has its own test.
                with patch.object(package, 'dynamic_symbols', return_value='0000 DF *UND* 0000 (GLIBC_2.34) memcpy'):
                    archive = package.package('v0.2.0', system, arch, binaries, root / 'dist')
                self.assertIn(archive.name, metadata.expected_assets('v0.2.0'))
                installation = root / 'install'
                with zipfile.ZipFile(archive) as binary_zip:
                    names = set(binary_zip.namelist())
                    self.assertFalse(any(name.startswith('assets/') for name in names))
                    self.assertTrue(set(files) <= names)
                    self.assertTrue(set(package.NOTICES) <= names)
                    self.assertEqual(binary_zip.read('LICENSE'), (ROOT / 'LICENSE').read_bytes())
                    # PDFium is embedded in the executable; only its notices ship.
                    self.assertIn('notices/pdfium/pdfium.txt', names)
                    self.assertFalse(any('pdfium' in name for name in names if not name.startswith('notices/')))
                    self.assertIn('config.yaml', names)
                    if system == 'windows':
                        self.assertEqual({name for name in names if not name.startswith('notices/')},
                                         {'papergo.exe', 'RELEASES.md', 'config.yaml', 'README.md', 'LICENSE', 'NOTICE'})
                    self.assertEqual((binary_zip.getinfo('papergo' + suffix).external_attr >> 16) & 0o777, 0o755)
                    binary_zip.extractall(installation)
                with zipfile.ZipFile(shared) as shared_zip:
                    self.assertEqual(set(shared_zip.namelist()), {'assets/' + name for name in models})
                    shared_zip.extractall(installation)
                for name in models:
                    self.assertEqual((installation / 'assets' / name).read_bytes(), (binaries / 'assets' / name).read_bytes())
                self.assertEqual((installation / 'assets/models/laya/model.onnx').read_bytes(), data)

    def test_symbol_requirements_are_the_highest_versions_by_prefix(self):
        objdump = '\n'.join([
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.2.5) free',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.38) __isoc23_strtol',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.9)  pipe2',
            '0000000000000000      DO *UND*  0000000000000000 (GLIBC_PRIVATE) _rtld_global',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBCXX_3.4.9) _ZNSo9_M_insertIdEERSoT_',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBCXX_3.4.32) _ZSt21ios_base_library_initv',
            '0000000000000000      DF *UND*  0000000000000000 (CXXABI_1.3.13) __cxa_init_primary_exception',
            '0000000000000000      DF *UND*  0000000000000000 (CXXABI_1.3) __cxa_begin_catch',
            '0000000000000000      DO *UND*  0000000000000000 (CXXABI_TM_1) _ITM_RU1',
            '0000000000000000      DF *UND*  0000000000000000 (GCC_3.0) _Unwind_Resume',
            '0000000000000000      DF *UND*  0000000000000000 (GCC_4.2.0) _Unwind_GetIPInfo',
        ])
        self.assertEqual(package.symbol_requirements(objdump),
                         {'GLIBC': (2, 38), 'GLIBCXX': (3, 4, 32), 'CXXABI': (1, 3, 13), 'GCC': (4, 2, 0)})
        readelf = '\n'.join([
            '    12: 0000000000000000     0 FUNC    GLOBAL DEFAULT  UND memcpy@GLIBC_2.14 (3)',
            '    13: 0000000000000000     0 FUNC    GLOBAL DEFAULT  UND _ZdlPv@GLIBCXX_3.4 (4)',
            '    14: 0000000000000000     0 FUNC    GLOBAL DEFAULT  UND _Unwind_Resume@GCC_3.0 (5)',
        ])
        self.assertEqual(package.symbol_requirements(readelf), {'GLIBC': (2, 14), 'GLIBCXX': (3, 4), 'GCC': (3, 0)})
        self.assertEqual(package.symbol_requirements('    1: 0 FUNC GLOBAL DEFAULT UND _ITM_RU1@CXXABI_TM_1'), {})
        # Versions compare numerically, not as text.
        self.assertGreater(package.symbol_requirements('(GLIBC_2.40)')['GLIBC'], package.SYSTEM_MAXIMUM['GLIBC'])
        self.assertLess(package.symbol_requirements('(GLIBC_2.4)')['GLIBC'], package.SYSTEM_MAXIMUM['GLIBC'])
        self.assertGreater(package.symbol_requirements('(GLIBCXX_3.4.100)')['GLIBCXX'], package.SYSTEM_MAXIMUM['GLIBCXX'])
        self.assertLess(package.symbol_requirements('(GLIBCXX_3.4.4)')['GLIBCXX'], package.SYSTEM_MAXIMUM['GLIBCXX'])
        # Ubuntu 24.04's and Debian 13's libstdc++6 and libgcc-s1 (GCC 14).
        self.assertEqual(package.SYSTEM_MAXIMUM, {'GLIBC': (2, 39), 'GLIBCXX': (3, 4, 33), 'CXXABI': (1, 3, 15), 'GCC': (14, 0, 0)})

    def test_linux_packages_refuse_extra_libraries_and_newer_system_libraries(self):
        inputs = {'papergo': Path('papergo'), 'lib/libonnxruntime.so': Path('ort'), 'notices/pdfium/pdfium.txt': Path('notice')}
        symbols = {'papergo': '(GLIBC_2.34) (GLIBC_2.39) (GLIBCXX_3.4.32) (CXXABI_1.3.15) (GCC_4.2.0)',
                   'ort': '(GLIBC_2.27) (GLIBCXX_3.4.22) (CXXABI_1.3.11) (GCC_3.0)'}
        with patch.object(package, 'dynamic_symbols', side_effect=lambda path: symbols[str(path)]) as read:
            package.check_linux(inputs)
            self.assertEqual(read.call_count, 2)
            # The compiler runtimes are the distribution's; none ships in lib/.
            for library in ('libstdc++.so.6', 'libgcc_s.so.1', 'libgomp.so.1'):
                with self.assertRaisesRegex(ValueError, re.escape('lib/' + library)):
                    package.check_linux({**inputs, 'lib/' + library: Path('ort')})
            for name, needs, message in [
                    ('ort', '(GLIBC_2.40)', 'lib/libonnxruntime.so needs GLIBC_2.40, newer than the glibc'),
                    ('papergo', '(GLIBCXX_3.4.34)', r'papergo needs GLIBCXX_3.4.34, newer than the libstdc\+\+ .*GLIBCXX_3.4.33'),
                    ('ort', '(CXXABI_1.3.16)', r'lib/libonnxruntime.so needs CXXABI_1.3.16, newer than the libstdc\+\+'),
                    ('papergo', '(GCC_15.0.0)', 'papergo needs GCC_15.0.0, newer than the libgcc_s')]:
                with self.subTest(needs=needs), patch.dict(symbols, {name: symbols[name] + ' ' + needs}):
                    with self.assertRaisesRegex(ValueError, message):
                        package.check_linux(inputs)
        with patch.object(package.shutil, 'which', return_value=None):
            with self.assertRaisesRegex(ValueError, 'objdump or readelf'):
                package.dynamic_symbols(Path('papergo'))

    def windows_binaries(self, binaries):
        for name in ['papergo.exe', *package.NOTICES, *package.WINDOWS_NOTICES]:
            (binaries / name).parent.mkdir(parents=True, exist_ok=True)
            (binaries / name).write_text(name)
        (binaries / package.VC_RUNTIME_NOTICE).write_text(VC_NOTICE.read_text())
        (binaries / 'notices/go/THIRD-PARTY-NOTICES').write_text('Go notices (windows/amd64)\n')

    def test_windows_package_refuses_dlls_and_missing_notices(self):
        with tempfile.TemporaryDirectory() as temp:
            binaries = Path(temp) / 'bin'
            self.windows_binaries(binaries)
            package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
            for stray in ['notices/onnxruntime.dll', 'lib/onnxruntime.dll']:
                (binaries / stray).parent.mkdir(parents=True, exist_ok=True)
                (binaries / stray).write_text('library')
                if stray.startswith('notices/'):
                    with self.assertRaisesRegex(ValueError, 'only papergo.exe'):
                        package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
                (binaries / stray).unlink()
            # Directories other than notices/ are not packaged at all on Windows.
            package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
            (binaries / 'notices/microsoft-visual-cpp-runtime.txt').unlink()
            with self.assertRaisesRegex(ValueError, 'notices'):
                package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')

    def test_windows_package_requires_the_visual_cpp_runtime_terms(self):
        text = ' '.join(VC_NOTICE.read_text().split())
        # The shipped notice states the pinned version and the terms' markers.
        for phrase in package.VC_RUNTIME_TERMS:
            self.assertIn(phrase, text)
        with tempfile.TemporaryDirectory() as temp:
            binaries = Path(temp) / 'bin'
            self.windows_binaries(binaries)
            notice = binaries / package.VC_RUNTIME_NOTICE
            for phrase in ['Visual C++ Runtime 14.44.35211 ', *package.VC_RUNTIME_TERMS]:
                with self.subTest(phrase=phrase):
                    self.assertIn(phrase, text)
                    # Line breaks inside a phrase do not matter; its absence does.
                    notice.write_text(text.replace(phrase, phrase.replace(' ', '\n', 2)))
                    package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
                    notice.write_text(text.replace(phrase, 'something else '))
                    with self.assertRaisesRegex(ValueError, re.escape(phrase.strip())):
                        package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')

    def test_packages_require_licences_and_go_npm_notices_for_their_platform(self):
        with tempfile.TemporaryDirectory() as temp:
            binaries = Path(temp) / 'bin'
            self.windows_binaries(binaries)
            for name in package.NOTICES:
                with self.subTest(missing=name):
                    original = (binaries / name).read_text()
                    (binaries / name).write_text('')
                    with self.assertRaisesRegex(ValueError, re.escape(name)):
                        package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
                    (binaries / name).unlink()
                    with self.assertRaisesRegex(ValueError, re.escape(name)):
                        package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')
                    (binaries / name).write_text(original)
            (binaries / 'notices/go/THIRD-PARTY-NOTICES').write_text('Go notices (linux/amd64)\n')
            with self.assertRaisesRegex(ValueError, 'not for windows/amd64'):
                package.package('v0.2.0', 'windows', 'amd64', binaries, Path(temp) / 'dist')

    def test_laya_cache_requires_pinned_manifest_and_every_file_hash(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'internal/decision').mkdir(parents=True)
            model = root / 'model'
            model.mkdir()
            data = b'pinned model weights'
            manifest = {'revision': 'supported', 'files': {'model.onnx': hashlib.sha256(data).hexdigest()}}
            (root / 'internal/decision/manifest.json').write_text(json.dumps(manifest))
            with patch.object(prepare, 'ROOT', root):
                self.assertFalse(prepare.verify_laya(model))
                (model / 'manifest.json').write_text(json.dumps(manifest))
                self.assertFalse(prepare.verify_laya(model))
                (model / 'model.onnx').write_bytes(data)
                self.assertTrue(prepare.verify_laya(model))
                (model / 'model.onnx').write_bytes(b'corrupted weights')
                self.assertFalse(prepare.verify_laya(model))
                (model / 'model.onnx').write_bytes(data)
                (model / 'manifest.json').write_text('{}')
                self.assertFalse(prepare.verify_laya(model))


if __name__ == '__main__':
    unittest.main()
