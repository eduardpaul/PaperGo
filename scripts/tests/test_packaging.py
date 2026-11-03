import hashlib
import importlib.util
import json
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
                with patch.object(build, 'compiler_runtime', return_value={}):
                    files = build.runtime_files(native, system, {})
                suffix = '.exe' if system == 'windows' else ''
                path = binaries / ('papergo' + suffix)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'application')
                path.chmod(0o755)
                assets.collect(binaries, files)
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
                    # PDFium is embedded in the executable; only its notices ship.
                    self.assertIn('notices/pdfium/pdfium.txt', names)
                    self.assertFalse(any('pdfium' in name for name in names if not name.startswith('notices/')))
                    self.assertIn('config.yaml', names)
                    self.assertEqual((binary_zip.getinfo('papergo' + suffix).external_attr >> 16) & 0o777, 0o755)
                    binary_zip.extractall(installation)
                with zipfile.ZipFile(shared) as shared_zip:
                    self.assertEqual(set(shared_zip.namelist()), {'assets/' + name for name in models})
                    shared_zip.extractall(installation)
                for name in models:
                    self.assertEqual((installation / 'assets' / name).read_bytes(), (binaries / 'assets' / name).read_bytes())
                self.assertEqual((installation / 'assets/models/laya/model.onnx').read_bytes(), data)

    def test_glibc_requirement_is_the_highest_glibc_symbol_version(self):
        objdump = '\n'.join([
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.2.5) free',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.38) __isoc23_strtol',
            '0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.9)  pipe2',
            '0000000000000000      DO *UND*  0000000000000000 (GLIBC_PRIVATE) _rtld_global',
            '00000000000a1b20 g    DF .text  0000000000000010  GLIBCXX_3.4.32 _ZNSt6thread4joinEv',
            '00000000000a1b20 g    DF .text  0000000000000010  GCC_3.0 _Unwind_Resume',
        ])
        self.assertEqual(package.glibc_requirement(objdump), (2, 38))
        readelf = '    12: 0000000000000000     0 FUNC    GLOBAL DEFAULT  UND memcpy@GLIBC_2.14 (3)'
        self.assertEqual(package.glibc_requirement(readelf), (2, 14))
        self.assertIsNone(package.glibc_requirement('    1: 0 FUNC GLOBAL DEFAULT UND _Unwind_Resume@GCC_3.0'))
        # Versions compare numerically, not as text.
        self.assertGreater(package.glibc_requirement('(GLIBC_2.40)'), package.GLIBC_MAXIMUM)
        self.assertLess(package.glibc_requirement('(GLIBC_2.4)'), package.GLIBC_MAXIMUM)

    def test_linux_packages_refuse_extra_libraries_and_newer_glibc(self):
        inputs = {'papergo': Path('papergo'), 'lib/libonnxruntime.so': Path('ort'),
                  'lib/libstdc++.so.6': Path('stdc++'), 'lib/libgcc_s.so.1': Path('gcc_s'),
                  'notices/pdfium/pdfium.txt': Path('notice')}
        symbols = {'papergo': '(GLIBC_2.34) (GLIBC_2.39)', 'ort': '(GLIBC_2.27)', 'stdc++': '(GLIBC_2.38)', 'gcc_s': '(GLIBC_2.35)'}
        with patch.object(package, 'dynamic_symbols', side_effect=lambda path: symbols[str(path)]) as read:
            package.check_linux(inputs)
            self.assertEqual(read.call_count, 4)
            with self.assertRaisesRegex(ValueError, 'lib/libgomp.so.1'):
                package.check_linux({**inputs, 'lib/libgomp.so.1': Path('gomp')})
            symbols['stdc++'] = '(GLIBC_2.38) (GLIBC_2.40)'
            with self.assertRaisesRegex(ValueError, r'lib/libstdc\+\+.so.6 needs GLIBC_2.40'):
                package.check_linux(inputs)
        with patch.object(package.shutil, 'which', return_value=None):
            with self.assertRaisesRegex(ValueError, 'objdump or readelf'):
                package.dynamic_symbols(Path('papergo'))

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
