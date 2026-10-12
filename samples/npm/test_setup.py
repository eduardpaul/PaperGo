"""Regression checks for NPM bootstrap and incomplete verification state."""
import importlib.util
import json
from pathlib import Path
import tempfile
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

    def test_unattended_two_factor_response_is_reported_without_exposing_challenge(self):
        api = Mock()
        api.call.side_effect = [{'setup': True}, {'requires_2fa': True, 'challenge_token': 'private-challenge'}]
        with patch.object(setup.sys.stdin, 'isatty', return_value=False), self.assertRaisesRegex(RuntimeError, 'two-factor') as error:
            setup.authenticate(api, LOGIN)
        self.assertNotIn('private-challenge', str(error.exception))


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
