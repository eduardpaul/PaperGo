"""Regression checks for NPM bootstrap and incomplete verification state."""
import importlib.util
import json
from pathlib import Path
import tempfile
import subprocess
import ssl
import unittest
from unittest.mock import Mock, patch
import urllib.error


def load_module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


setup = load_module('setup')
verify = load_module('verify')
LOGIN = {'npm_email': 'operator@example.test', 'npm_password': 'private-test-password'}


class StartupTests(unittest.TestCase):
    def test_startup_failure_prints_logs_and_actionable_error(self):
        with patch.object(setup.subprocess, 'run', side_effect=[
                subprocess.CalledProcessError(1, ['docker', 'compose', 'up']),
                subprocess.CompletedProcess([], 0)]) as run, patch('builtins.print'):
            with self.assertRaisesRegex(RuntimeError, 'schema checksum mismatch') as error:
                setup.start_stack(no_build=False)
        self.assertIn('samples/npm/README.md', str(error.exception))
        startup, logs = run.call_args_list
        self.assertIn('--build', startup.args[0])
        self.assertTrue(startup.kwargs['check'])
        self.assertEqual(logs.args[0][-8:],
                         ['logs', '--no-color', '--tail', '80',
                          'volume-permissions', 'papergo', 'papergo-mcp', 'npm'])
        self.assertFalse(logs.kwargs['check'])

    def test_log_failure_keeps_startup_error(self):
        with patch.object(setup.subprocess, 'run', side_effect=[
                subprocess.CalledProcessError(17, ['docker', 'compose', 'up']),
                OSError('logs unavailable')]), patch('builtins.print'):
            with self.assertRaisesRegex(RuntimeError, 'exit 17'):
                setup.start_stack(no_build=True)

    def test_successful_startup_without_build_does_not_fetch_logs(self):
        with patch.object(setup.subprocess, 'run') as run:
            setup.start_stack(no_build=True)
        run.assert_called_once()
        self.assertNotIn('--build', run.call_args.args[0])


class SingleBinaryTests(unittest.TestCase):
    def test_compose_has_no_initialize_service_and_mounts_proxy_zvec_config(self):
        root = Path(__file__).parent
        for name in ('compose.yaml', 'setup.py', 'verify.py'):
            text = (root / name).read_text()
            self.assertNotIn('papergo-admin initialize', text)
            self.assertNotRegex(text, r'(?m)^  initialize:')
        compose = (root / 'compose.yaml').read_text()
        self.assertIn('./generated/config.yaml:/config/config.yaml:ro', compose)
        self.assertIn('PAPERGO_CONFIG: /config/config.yaml', compose)
        self.assertNotIn('AUTH_MODE:', compose)
        config = setup.papergo_config('https://docs.example.com', 'papergo-admin', '10.90.0.2/32')
        self.assertIn('  mode: proxy', config)
        self.assertIn('  proxy_trusted_cidrs:\n    - "10.90.0.2/32"', config)
        self.assertIn('  store: zvec', config)
        self.assertIn('  admin_user: "papergo-admin"', config)
        self.assertIn('mcp:\n  public_url: "https://docs.example.com/v1/mcp"', config)

    def test_assistants_reach_the_mcp_server_past_the_access_list(self):
        compose = (Path(__file__).parent / 'compose.yaml').read_text()
        self.assertIn('dockerfile: extensions/mcp/Dockerfile', compose)
        self.assertIn('PAPERGO_MCP_BACKEND_URL: http://papergo:8080', compose)
        for port in (443, 8443):
            config = setup.proxy_server_config('https://docs.example.com', port)
            location = config[config.index('location = /v1/mcp {'):]
            self.assertIn('auth_basic off;', location)
            self.assertIn('proxy_pass http://papergo-mcp:8081;', location)
            # No proxy identity reaches the MCP server.
            self.assertIn('proxy_set_header X-Papergo-Subject "";', location)
            self.assertIn('proxy_set_header X-Papergo-Proxy-Secret "";', location)
            self.assertEqual('return 308' in config, port != 443)


class EgressTests(unittest.TestCase):
    def test_checks_dns_and_verified_https_inside_papergo(self):
        with patch.object(setup, 'compose') as compose:
            setup.verify_egress()
        compose.assert_called_once_with('exec', '-T', 'papergo', '/opt/papergo/papergo-admin', 'health',
                                        '--url', 'https://calendar.google.com/', '--timeout', '15s')

    def test_failed_egress_has_actionable_network_error(self):
        with patch.object(setup, 'compose', side_effect=subprocess.CalledProcessError(1, ['docker'])):
            with self.assertRaisesRegex(RuntimeError, 'outbound DNS and TCP 443'):
                setup.verify_egress()


class SearchStoreTests(unittest.TestCase):
    def test_requires_active_zvec_store_on_verified_connection(self):
        for store in ['zvec', 'sqlite']:
            connection = Mock()
            connection.getresponse.return_value.status = 200
            connection.getresponse.return_value.read.return_value = json.dumps({'store': store}).encode()
            with patch.object(setup.ssl, 'create_default_context'), patch.object(setup.http.client, 'HTTPSConnection', return_value=connection):
                if store == 'zvec':
                    setup.verify_search_store({'host': 'docs.example', 'https_port': 443, 'tls': 'letsencrypt'}, {'papergo_user': 'admin', 'papergo_password': 'private'})
                else:
                    with self.assertRaisesRegex(RuntimeError, 'zvec search store'):
                        setup.verify_search_store({'host': 'docs.example', 'https_port': 443, 'tls': 'letsencrypt'}, {'papergo_user': 'admin', 'papergo_password': 'private'})
            connection.close.assert_called_once()
            self.assertEqual(connection.request.call_args.args, ('GET', '/v1/search/status'))


class AuthenticationTests(unittest.TestCase):
    def test_new_instance_creates_first_admin_before_login(self):
        api = Mock()
        api.call.side_effect = [{'setup': False}, {'id': 1}, {'token': 'test-token'}]
        setup.authenticate(api, LOGIN)
        self.assertEqual([call.args[:2] for call in api.call.call_args_list],
                         [('GET', '/'), ('POST', '/users'), ('POST', '/tokens')])
        self.assertEqual(api.token, 'test-token')

    def test_existing_instance_only_logs_in(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, {'token': 'test-token'}]
        setup.authenticate(api, LOGIN)
        self.assertEqual(api.call.call_args.args[2]['identity'], LOGIN['npm_email'])
        self.assertEqual(api.call.call_count, 2)

    def test_bad_existing_credentials_never_create_another_user(self):
        for status in (400, 401, 403):
            api = Mock()
            api.call.side_effect = [{'setup': True}, urllib.error.HTTPError(
                'http://localhost/api/tokens', status, LOGIN['npm_password'], {}, None)]
            with patch.object(setup.sys.stdin, 'isatty', return_value=False), self.assertRaisesRegex(RuntimeError, 'generated/credentials.json') as error:
                setup.authenticate(api, LOGIN)
            self.assertNotIn(LOGIN['npm_password'], str(error.exception))
            self.assertEqual(api.call.call_count, 2)

    def test_existing_login_prompt_configures_with_supplied_credentials(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, urllib.error.HTTPError(
            'http://localhost/api/tokens', 400, 'rejected', {}, None), {'token': 'test-token'}]
        with patch.object(setup.sys.stdin, 'isatty', return_value=True), patch('builtins.input', return_value='existing@example.test'), patch.object(setup.getpass, 'getpass', return_value='existing-password'), patch('builtins.print'):
            credentials = setup.authenticate(api, {**LOGIN, 'papergo_password': 'application-password'})
        self.assertEqual(api.call.call_args.args, ('POST', '/tokens', {'identity': 'existing@example.test', 'secret': 'existing-password'}))
        self.assertEqual(credentials['npm_password'], 'existing-password')
        self.assertEqual(credentials['papergo_password'], 'application-password')
        self.assertEqual(api.token, 'test-token')
        self.assertNotIn(('POST', '/users'), [call.args[:2] for call in api.call.call_args_list])

    def test_two_factor_code_completes_login(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, {'requires_2fa': True, 'challenge_token': 'test-challenge'}, {'token': 'test-token'}]
        with patch.object(setup.sys.stdin, 'isatty', return_value=True), patch.object(setup.getpass, 'getpass', return_value='123456'):
            setup.authenticate(api, LOGIN)
        self.assertEqual(api.call.call_args.args, ('POST', '/tokens/2fa', {'challenge_token': 'test-challenge', 'code': '123456'}))
        self.assertEqual(api.token, 'test-token')

    def test_rejected_prompt_explains_management_password_and_reset_option(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, urllib.error.HTTPError(
            'http://localhost/api/tokens', 400, 'rejected', {}, None), urllib.error.HTTPError(
            'http://localhost/api/tokens', 400, 'private-password', {}, None)]
        with patch.object(setup.sys.stdin, 'isatty', return_value=True), patch('builtins.input', return_value=LOGIN['npm_email']), patch.object(setup.getpass, 'getpass', return_value='private-password'), patch('builtins.print'):
            with self.assertRaisesRegex(RuntimeError, '--reset-npm-admin-password') as error:
                setup.authenticate(api, LOGIN)
        self.assertNotIn('private-password', str(error.exception))
        self.assertEqual(api.call.call_count, 3)

    def test_unattended_two_factor_response_is_reported_without_exposing_challenge(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, {'requires_2fa': True, 'challenge_token': 'private-challenge'}]
        with patch.object(setup.sys.stdin, 'isatty', return_value=False), self.assertRaisesRegex(RuntimeError, 'two-factor') as error:
            setup.authenticate(api, LOGIN)
        self.assertNotIn('private-challenge', str(error.exception))


class ResetTests(unittest.TestCase):
    def test_reset_passes_credentials_only_over_stdin(self):
        with patch.object(setup.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as run, patch('builtins.print'):
            setup.reset_management_password(LOGIN)
        command = run.call_args.args[0]
        self.assertNotIn(LOGIN['npm_password'], ' '.join(command))
        self.assertNotIn(LOGIN['npm_email'], ' '.join(command))
        self.assertEqual(json.loads(run.call_args.kwargs['input']),
                         {'email': LOGIN['npm_email'], 'password': LOGIN['npm_password']})
        self.assertTrue(run.call_args.kwargs['capture_output'])

    def test_reset_failure_does_not_print_container_output(self):
        with patch.object(setup.subprocess, 'run', return_value=subprocess.CompletedProcess(
                [], 2, stdout=LOGIN['npm_password'], stderr=LOGIN['npm_password'])):
            with self.assertRaisesRegex(RuntimeError, 'active administrator') as error:
                setup.reset_management_password(LOGIN)
        self.assertNotIn(LOGIN['npm_password'], str(error.exception))


class AccessListTests(unittest.TestCase):
    def test_existing_list_configures_admin_and_keeps_other_users_and_address_rules(self):
        api = Mock()
        api.call.side_effect = [[{'id': 3, 'name': 'PaperGo users'}],
                               {'id': 3, 'items': [{'username': 'papergo-admin'}, {'username': 'member'}]},
                               {'id': 3}]
        setup.configure_access_list(api, 'papergo-admin', 'application-password')
        method, path, payload = api.call.call_args.args
        self.assertEqual((method, path), ('PUT', '/nginx/access-lists/3'))
        self.assertEqual(payload['items'], [{'username': 'member', 'password': ''},
                                           {'username': 'papergo-admin', 'password': 'application-password'}])
        self.assertFalse(payload['pass_auth'])
        self.assertFalse(payload['satisfy_any'])
        self.assertNotIn('clients', payload)


class ProxyReadinessTests(unittest.TestCase):
    settings = {'host': 'papergo.localhost', 'https_port': 18443, 'tls': 'local'}
    credentials = {'papergo_user': 'papergo-admin', 'papergo_password': 'test-password'}

    def test_waits_through_untrusted_and_old_certificates_before_accepting_profile(self):
        context = Mock()
        old_socket, new_socket = Mock(), Mock()
        old_socket.getpeercert.return_value = b'old-certificate'
        new_socket.getpeercert.return_value = b'new-certificate'
        context.wrap_socket.side_effect = [ssl.SSLCertVerificationError(1, 'old CA'), old_socket, new_socket]
        connection = Mock()
        connection.getresponse.return_value.status = 200
        connection.getresponse.return_value.read.return_value = b'{"subject":"papergo-admin"}'
        certificate = Mock()
        certificate.read_text.return_value = 'test certificate'
        with patch.object(setup.ssl, 'create_default_context', return_value=context), patch.object(setup.ssl, 'PEM_cert_to_DER_cert', return_value=b'new-certificate'), patch.object(setup.http.client, 'HTTPSConnection', return_value=connection), patch.object(setup.socket, 'create_connection'), patch.object(setup.time, 'sleep') as sleep, patch.object(setup.time, 'monotonic', return_value=0):
            setup.wait_for_proxy(self.settings, self.credentials, certificate)
        self.assertEqual(context.wrap_socket.call_count, 3)
        connection.request.assert_called_once()
        self.assertEqual(connection.request.call_args.args, ('GET', '/v1/identity/me'))
        self.assertEqual(connection.close.call_count, 3)
        self.assertEqual(sleep.call_count, 2)

    def test_persistent_certificate_failure_never_reports_ready(self):
        context = Mock()
        context.wrap_socket.side_effect = ssl.SSLCertVerificationError(1, 'wrong CA')
        with patch.object(setup.ssl, 'create_default_context', return_value=context), patch.object(setup.http.client, 'HTTPSConnection'), patch.object(setup.socket, 'create_connection'), patch.object(setup.time, 'sleep'), patch.object(setup.time, 'monotonic', side_effect=[0, 0, 0, 1]):
            with self.assertRaisesRegex(RuntimeError, 'served TLS certificate does not verify'):
                setup.wait_for_proxy(self.settings, self.credentials, timeout=1)

    def test_wrong_authenticated_subject_never_reports_ready(self):
        connection = Mock()
        connection.getresponse.return_value.status = 200
        connection.getresponse.return_value.read.return_value = b'{"subject":"someone-else"}'
        with patch.object(setup.ssl, 'create_default_context'), patch.object(setup.http.client, 'HTTPSConnection', return_value=connection), patch.object(setup.socket, 'create_connection'), patch.object(setup.time, 'sleep'), patch.object(setup.time, 'monotonic', side_effect=[0, 0, 0, 1]):
            with self.assertRaisesRegex(RuntimeError, 'authenticated profile is not ready'):
                setup.wait_for_proxy(self.settings, self.credentials, timeout=1)


class VerificationTests(unittest.TestCase):
    def test_missing_setup_completion_is_reported_before_loading_CA(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(verify, 'ROOT', Path(directory)):
            generated = Path(directory) / 'generated'
            generated.mkdir()
            (generated / 'deployment.json').write_text(json.dumps(
                {'host': 'papergo.localhost', 'https_port': 18443, 'tls': 'local'}))
            with self.assertRaisesRegex(RuntimeError, 'setup is incomplete'):
                verify.load_deployment()

    def test_missing_local_CA_is_a_clear_setup_error(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(verify, 'ROOT', Path(directory)):
            generated = Path(directory) / 'generated'
            generated.mkdir()
            (generated / 'deployment.json').write_text(json.dumps(
                {'host': 'papergo.localhost', 'https_port': 18443, 'tls': 'local'}))
            (generated / 'ready').write_text('https://papergo.localhost:18443\n')
            (generated / 'credentials.json').write_text(json.dumps(LOGIN))
            with self.assertRaisesRegex(RuntimeError, 'setup is incomplete'):
                verify.load_deployment()


if __name__ == '__main__':
    unittest.main()
