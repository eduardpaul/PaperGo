import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release_image', Path(__file__).parents[1] / 'release-image.py')
image = importlib.util.module_from_spec(spec)
spec.loader.exec_module(image)


def manifest(tag='v1.2.3', revision='commit'):
    return {'annotations': {'org.opencontainers.image.version': tag, 'org.opencontainers.image.revision': revision},
            'manifests': [{'platform': {'os': 'linux', 'architecture': arch}} for arch in ['amd64', 'arm64']] +
                         [{'platform': {'os': 'unknown', 'architecture': 'unknown'}}]}


class ReleaseImageTests(unittest.TestCase):
    def test_normalized_package_name_and_invalid_repositories(self):
        self.assertEqual(image.image_name('Owner/PaperGo'), 'ghcr.io/owner/papergo')
        self.assertEqual(image.image_name('Owner/PaperGo', 'papergo-mcp'), 'ghcr.io/owner/papergo-mcp')
        with self.assertRaises(ValueError):
            image.image_name('Owner/PaperGo', '../other')
        for repository in ['../repo', 'owner/repo;exit 1', 'owner/repo/sub']:
            with self.assertRaises(ValueError):
                image.image_name(repository)

    def test_first_package_and_authorization_errors_are_distinct(self):
        with patch.object(image.subprocess, 'check_output', return_value='User\n'), \
             patch.object(image.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', 'gh: Not Found (HTTP 404)')):
            self.assertFalse(image.package_exists('Owner/PaperGo'))
            image.package_exists('Owner/PaperGo', 'papergo-mcp')
            self.assertEqual(image.subprocess.run.call_args.args[0][-1], 'users/owner/packages/container/papergo-mcp')
        with patch.object(image.subprocess, 'check_output', return_value='Organization\n'), \
             patch.object(image.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', 'gh: Forbidden (HTTP 403)')):
            with self.assertRaises(RuntimeError):
                image.package_exists('Owner/PaperGo')

    def test_registry_errors_do_not_masquerade_as_missing_tags(self):
        with patch.object(image.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', 'manifest unknown')):
            self.assertIsNone(image.inspect('registry/image', 'v1.2.3'))
        with patch.object(image.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', '403 Forbidden')):
            with self.assertRaises(RuntimeError):
                image.inspect('registry/image', 'v1.2.3')

    def test_preserves_a_completed_image_on_retry(self):
        with patch.object(image, 'inspect', return_value=manifest()), patch.object(image.subprocess, 'run') as run:
            image.publish('registry/image', 'owner/repo', 'v1.2.3', 'commit', 'missing-digests')
            run.assert_not_called()
        for wrong in [manifest(revision='other'), manifest(tag='v1.2.4')]:
            with patch.object(image, 'inspect', return_value=wrong), self.assertRaises(ValueError):
                image.publish('registry/image', 'owner/repo', 'v1.2.3', 'commit', 'missing-digests')

    def test_missing_architecture_is_rejected(self):
        incomplete = manifest()
        incomplete['manifests'].pop(1)
        with self.assertRaises(ValueError):
            image.validate(incomplete, 'v1.2.3', 'commit')

    def test_publish_uses_both_tested_digests_and_checks_the_result(self):
        with tempfile.TemporaryDirectory() as directory:
            for arch, digit in [('amd64', 'a'), ('arm64', 'b')]:
                (Path(directory) / arch).write_text('sha256:' + digit * 64)
            with patch.object(image, 'inspect', side_effect=[None, manifest()]), patch.object(image.subprocess, 'run') as run:
                image.publish('registry/image', 'owner/repo', 'v1.2.3', 'commit', directory)
            args = run.call_args.args[0]
            self.assertEqual(args[-2:], ['registry/image@sha256:' + 'a' * 64, 'registry/image@sha256:' + 'b' * 64])
            self.assertIn('index:org.opencontainers.image.revision=commit', args)
            (Path(directory) / 'arm64').write_text('not-a-digest')
            with patch.object(image, 'inspect', return_value=None), self.assertRaises(ValueError):
                image.publish('registry/image', 'owner/repo', 'v1.2.3', 'commit', directory)

    def test_prereleases_and_old_stable_retries_never_move_latest(self):
        with patch.object(image.subprocess, 'check_output') as query, patch.object(image.subprocess, 'run') as run:
            image.promote_latest('registry/image', 'owner/repo', 'v2.0.0-rc.1')
            query.assert_not_called()
            run.assert_not_called()
        with patch.object(image.subprocess, 'check_output', return_value=json.dumps({'tag_name': 'v2.0.0'})), \
             patch.object(image.subprocess, 'run') as run:
            image.promote_latest('registry/image', 'owner/repo', 'v1.2.3')
            run.assert_not_called()
            image.promote_latest('registry/image', 'owner/repo', 'v2.0.0')
            self.assertEqual(run.call_args.args[0][-1], 'registry/image:v2.0.0')


if __name__ == '__main__':
    unittest.main()
