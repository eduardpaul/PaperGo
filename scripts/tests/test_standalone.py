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


build = module('standalone_build', 'build-standalone.py')
prepare = module('preset_models', 'prepare-preset-models.py')


class StandaloneTests(unittest.TestCase):
    def test_streamed_payload_round_trip_and_integrity_manifest(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / 'weights'
            data = bytes(range(256)) * 8193  # Several streaming blocks.
            source.write_bytes(data)
            archive = root / 'runtime.zip'
            build.bundle(archive, {'papergo-core': source, 'models/laya/model.onnx': source}, 'papergo-core')
            with zipfile.ZipFile(archive) as bundle:
                manifest = json.loads(bundle.read('runtime.json'))
                self.assertEqual(manifest['program'], 'papergo-core')
                for name, info in manifest['files'].items():
                    self.assertEqual(bundle.read(name), data)
                    self.assertEqual(info['sha256'], hashlib.sha256(data).hexdigest())
                    self.assertEqual(info['size'], len(data))
                    self.assertEqual(info['mode'], 0o700 if name == 'papergo-core' else 0o600)

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
