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
                for name in ('papergo', 'papergo-admin', 'papergo-mcp'):
                    path = binaries / (name + suffix)
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
                database = root / 'release-database.json'
                database.write_text(json.dumps({'releases': [{'version': 'v0.2.0'}]}))
                archive = package.package('v0.2.0', system, arch, binaries, database, root / 'dist')
                self.assertIn(archive.name, metadata.expected_assets('v0.2.0'))
                installation = root / 'install'
                with zipfile.ZipFile(archive) as binary_zip:
                    names = set(binary_zip.namelist())
                    self.assertFalse(any(name.startswith('assets/') for name in names))
                    self.assertTrue(set(files) <= names)
                    self.assertIn('config.yaml', names)
                    self.assertEqual((binary_zip.getinfo('papergo' + suffix).external_attr >> 16) & 0o777, 0o755)
                    binary_zip.extractall(installation)
                with zipfile.ZipFile(shared) as shared_zip:
                    self.assertEqual(set(shared_zip.namelist()), {'assets/' + name for name in models})
                    shared_zip.extractall(installation)
                for name in models:
                    self.assertEqual((installation / 'assets' / name).read_bytes(), (binaries / 'assets' / name).read_bytes())
                self.assertEqual((installation / 'assets/models/laya/model.onnx').read_bytes(), data)
                database.write_text(json.dumps({'releases': [{'version': 'v0.1.0'}]}))
                with self.assertRaisesRegex(ValueError, 'does not match'):
                    package.package('v0.2.0', system, arch, binaries, database, root / 'dist')

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
