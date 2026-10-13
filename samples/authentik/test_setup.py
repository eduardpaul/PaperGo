"""Regression checks for the authentik sample's generated configuration and incomplete verification state."""
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


def load_module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


setup = load_module('setup')
verify = load_module('verify')
LOCAL = {'host': 'papergo.localhost', 'auth_host': 'auth.papergo.localhost', 'email': 'admin@example.test',
         'tls': 'local', 'admin_user': 'papergo-admin', 'http_port': 18080, 'https_port': 18443}
PUBLIC = {**LOCAL, 'host': 'docs.example.com', 'auth_host': 'auth.example.com', 'tls': 'letsencrypt',
          'http_port': 80, 'https_port': 443}


class GeneratedConfigurationTests(unittest.TestCase):
    def test_issuer_is_the_per_provider_url_on_the_public_origin(self):
        self.assertEqual(setup.issuer(PUBLIC), 'https://auth.example.com/application/o/papergo/')
        self.assertEqual(setup.issuer(LOCAL), 'https://auth.papergo.localhost:18443/application/o/papergo/')

    def test_local_proxy_uses_generated_certificate_and_redirects_to_custom_port(self):
        caddyfile = setup.caddyfile(LOCAL)
        self.assertIn('http_port 18080', caddyfile)
        self.assertIn('https_port 18443', caddyfile)
        self.assertEqual(caddyfile.count('tls /etc/caddy/tls/server.crt /etc/caddy/tls/server.key'), 2)
        self.assertIn('redir https://{host}:18443{uri} 308', caddyfile)
        self.assertNotIn('email', caddyfile)

    def test_public_proxy_requests_certificates_and_streams_papergo(self):
        caddyfile = setup.caddyfile(PUBLIC)
        self.assertIn('email admin@example.test', caddyfile)
        self.assertNotIn('tls /etc/caddy', caddyfile)
        self.assertNotIn('redir', caddyfile)
        self.assertIn('docs.example.com {', caddyfile)
        self.assertIn('reverse_proxy papergo:8080 {\n\t\tflush_interval -1', caddyfile)
        self.assertIn('auth.example.com {', caddyfile)
        self.assertIn('reverse_proxy authentik-server:9000', caddyfile)

    def test_password_hash_is_a_django_pbkdf2_hash_of_the_password(self):
        encoded = setup.password_hash('correct horse', iterations=1000)
        algorithm, iterations, salt, digest = encoded.split('$')
        self.assertEqual((algorithm, iterations), ('pbkdf2_sha256', '1000'))
        expected = hashlib.pbkdf2_hmac('sha256', b'correct horse', salt.encode(), 1000)
        self.assertEqual(base64.b64decode(digest), expected)
        self.assertNotEqual(setup.password_hash('correct horse', iterations=1000), encoded, 'salts must be random')

    def test_env_file_quotes_values_so_hashes_are_not_interpolated(self):
        self.assertEqual(setup.env_file({'HASH': 'pbkdf2_sha256$1$salt$digest'}), "HASH='pbkdf2_sha256$1$salt$digest'\n")
        for value in ("it's", 'two\nlines'):
            with self.assertRaises(RuntimeError):
                setup.env_file({'VALUE': value})


class ArgumentTests(unittest.TestCase):
    def run_setup(self, *argv):
        with tempfile.TemporaryDirectory() as directory, patch.object(setup, 'GENERATED', Path(directory) / 'generated'), \
                patch('sys.argv', ['setup.py', *argv]), patch.object(setup, 'compose') as compose, \
                patch('sys.stderr'), self.assertRaises(SystemExit):
            setup.main()
        compose.assert_not_called()

    def test_rejects_unsafe_or_conflicting_settings_before_starting_containers(self):
        base = ['--email', 'admin@example.test']
        self.run_setup(*base, '--host', 'docs.example.com')
        self.run_setup(*base, '--host', 'docs.example.com', '--auth-host', 'docs.example.com')
        self.run_setup(*base, '--host', 'https://docs.example.com', '--auth-host', 'auth.example.com')
        self.run_setup(*base, '--host', 'papergo.localhost', '--auth-host', 'auth.papergo.localhost')
        self.run_setup(*base, '--host', 'docs.example.com', '--auth-host', 'auth.example.com', '--http-port', '8080')
        self.run_setup(*base, '--host', 'docs.example.com', '--auth-host', 'auth.example.com', '--admin-user', 'akadmin')
        self.run_setup('--email', "o'brien@example.test", '--host', 'docs.example.com', '--auth-host', 'auth.example.com')


class VerificationStateTests(unittest.TestCase):
    def test_verification_requires_completed_setup(self):
        with tempfile.TemporaryDirectory() as directory:
            generated = Path(directory)
            (generated / 'deployment.json').write_text(json.dumps(LOCAL))
            (generated / 'credentials.json').write_text('{}')
            with patch.object(verify, 'GENERATED', generated), self.assertRaisesRegex(RuntimeError, 'incomplete'):
                verify.load_deployment()
            (generated / 'ready').write_text('https://other.example\n')
            with patch.object(verify, 'GENERATED', generated), self.assertRaisesRegex(RuntimeError, 'rerun setup.py'):
                verify.load_deployment()

    def test_token_claims_are_read_from_the_payload(self):
        payload = base64.urlsafe_b64encode(json.dumps({'aud': 'papergo', 'groups': ['papergo-admins']}).encode()).rstrip(b'=').decode()
        self.assertEqual(verify.claims(f'header.{payload}.signature')['groups'], ['papergo-admins'])


if __name__ == '__main__':
    unittest.main()
