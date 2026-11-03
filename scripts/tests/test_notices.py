import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).parents[2]
NPM_NOTICES = ROOT / 'scripts/npm-notices.mjs'
spec = importlib.util.spec_from_file_location('go_notices', ROOT / 'scripts/generate-go-notices.py')
go_notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(go_notices)

MIT = ('Permission is hereby granted, free of charge, to any person obtaining a copy\n'
       'of this software and associated documentation files (the "Software")...\n')
BSD2 = ('Redistribution and use in source and binary forms, with or without\n'
        'modification, are permitted provided that the following conditions are met:\n')
BSD3 = BSD2 + 'Neither the name of the copyright holder nor the names of its contributors\n'
GPL = 'GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n... GNU General Public License ...\n'


class GoNoticesTests(unittest.TestCase):
    def test_modules_skip_the_main_module_and_follow_replacements(self):
        listing = '\n'.join([
            'papergo\t\ttrue\t/src\t\t\t',
            'github.com/a/b\tv1.2.0\tfalse\t/mod/github.com/a/b@v1.2.0\t\t\t',
            'github.com/a/b\tv1.2.0\tfalse\t/mod/github.com/a/b@v1.2.0\t\t\t',
            'example.com/fork\tv1.0.0\tfalse\t/mod/example.com/fork@v1.0.0\texample.com/patched\tv1.0.1\t/mod/example.com/patched@v1.0.1',
            'example.com/local\tv0.1.0\tfalse\t/src/local\t../local\t\t/src/local',
            '',
        ])
        self.assertEqual(go_notices.parse_modules(listing), {
            'github.com/a/b': ('v1.2.0', '/mod/github.com/a/b@v1.2.0'),
            'example.com/fork': ('v1.0.0 => example.com/patched v1.0.1', '/mod/example.com/patched@v1.0.1'),
            'example.com/local': ('v0.1.0 => ../local', '/src/local'),
        })

    def test_licence_files_are_the_module_root_licence_copying_and_notice_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in ['LICENSE', 'LICENCE.md', 'LICENSE-GO', 'COPYING', 'NOTICE.txt', 'PATENTS', 'UNLICENSE',
                         'notice.go', 'notice_test.go', 'license_test.go', 'README.md', 'licensing.txt']:
                (root / name).write_text(name)
            (root / 'LICENSES').mkdir()
            (root / 'sub').mkdir()
            (root / 'sub/LICENSE').write_text('nested')
            self.assertEqual([name for name, _ in go_notices.licence_files(root)],
                             ['COPYING', 'LICENCE.md', 'LICENSE', 'LICENSE-GO', 'NOTICE.txt', 'PATENTS', 'UNLICENSE'])

    def test_classify_recognises_allowed_and_refuses_other_licences(self):
        self.assertEqual(go_notices.classify('m', [('LICENSE', MIT)]), ['MIT'])
        self.assertEqual(go_notices.classify('m', [('LICENSE', BSD2)]), ['BSD-2-Clause'])
        self.assertEqual(go_notices.classify('m', [('LICENSE', BSD3)]), ['BSD-3-Clause'])
        self.assertEqual(go_notices.classify('m', [('LICENSE', 'Apache License\n  Version 2.0, January 2004\n'), ('NOTICE', 'Copyright')]),
                         ['Apache-2.0'])
        self.assertEqual(go_notices.classify('m', [('LICENSE', MIT + BSD3)]), ['MIT', 'BSD-3-Clause'])
        with self.assertRaisesRegex(ValueError, 'm has no LICENSE'):
            go_notices.classify('m', [])
        with self.assertRaisesRegex(ValueError, 'm is under GPL'):
            go_notices.classify('m', [('LICENSE', MIT), ('COPYING', GPL)])
        with self.assertRaisesRegex(ValueError, 'm is under MPL'):
            go_notices.classify('m', [('LICENSE', 'Mozilla Public License Version 2.0')])
        with self.assertRaisesRegex(ValueError, 'do not recognise'):
            go_notices.classify('m', [('LICENSE', 'All rights reserved.')])

    def test_reviewed_modules_are_pinned_to_their_licence_files(self):
        files = [('LICENSE', MIT + 'Creative Commons Attribution 4.0 for the documentation')]
        with self.assertRaisesRegex(ValueError, 'Creative Commons'):
            go_notices.classify('m', files)
        reviewed = {'m': ('MIT', go_notices.licence_digest(files))}
        with patch.dict(go_notices.REVIEWED, reviewed):
            self.assertEqual(go_notices.classify('m', files), ['MIT'])
            with self.assertRaisesRegex(ValueError, 'changed since they were reviewed'):
                go_notices.classify('m', [('LICENSE', MIT)])

    def test_render_lists_each_component_with_its_licence_texts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name, text in [('go/LICENSE', BSD3), ('go/PATENTS', 'patents'), ('a/LICENSE', MIT), ('a/NOTICE', 'notice of a')]:
                (root / name).parent.mkdir(exist_ok=True)
                (root / name).write_text(text)
            text = go_notices.render('linux/arm64', [('Go standard library', 'go1.27.0', root / 'go'),
                                                    ('github.com/a/b', 'v1.2.0', root / 'a')])
        self.assertTrue(text.startswith('Third-party notices for the Go code linked into papergo (linux/arm64)\n'))
        self.assertIn('the Go standard library and 1 modules.', text)
        self.assertIn('  github.com/a/b v1.2.0 (MIT)\n', text)
        self.assertIn('github.com/a/b v1.2.0\nLicence: MIT\n\n--- LICENSE ---\n\n' + MIT.rstrip() + '\n\n--- NOTICE ---\n\nnotice of a\n', text)
        self.assertIn('Go standard library go1.27.0\nLicence: BSD-3-Clause\n', text)
        self.assertIn('--- PATENTS ---\n\npatents\n', text)


@unittest.skipUnless(shutil.which('node'), 'needs Node.js')
class NpmNoticesTests(unittest.TestCase):
    def node(self, script):
        return subprocess.run(['node', '--input-type=module', '-e', script], check=True, capture_output=True, text=True).stdout

    def test_spdx_expressions_need_an_allowed_choice(self):
        expressions = ['MIT', 'Apache-2.0', '0BSD', '(MIT AND Zlib)', '(MPL-2.0 OR Apache-2.0)', 'MIT OR GPL-3.0-or-later',
                       'Apache-2.0 WITH LLVM-exception', '(MIT OR CC0-1.0) AND BSD-3-Clause',
                       'GPL-3.0', 'MIT AND GPL-2.0', 'Unlicense', 'CC-BY-4.0', '(MIT AND CC-BY-4.0) OR GPL-2.0']
        results = json.loads(self.node(f'import {{ allowed }} from {json.dumps(NPM_NOTICES.as_uri())};'
                                       f'console.log(JSON.stringify({json.dumps(expressions)}.map(allowed)))'))
        self.assertEqual(dict(zip(expressions, results)), {
            'MIT': True, 'Apache-2.0': True, '0BSD': True, '(MIT AND Zlib)': True, '(MPL-2.0 OR Apache-2.0)': True,
            'MIT OR GPL-3.0-or-later': True, 'Apache-2.0 WITH LLVM-exception': True, '(MIT OR CC0-1.0) AND BSD-3-Clause': True,
            'GPL-3.0': False, 'MIT AND GPL-2.0': False, 'Unlicense': False, 'CC-BY-4.0': False, '(MIT AND CC-BY-4.0) OR GPL-2.0': False})

    def project(self, root, packages):
        """A project whose lockfile lists packages, {path: (lock entry, package.json or None, files)}."""
        lock = {'': {'name': 'app'}}
        for path, (entry, manifest, files) in packages.items():
            lock[path] = entry
            if manifest is not None:
                (root / path).mkdir(parents=True)
                (root / path / 'package.json').write_text(json.dumps(manifest))
                for name, text in files.items():
                    (root / path / name).write_text(text)
        (root / 'package.json').write_text(json.dumps({'name': '@papergo/app'}))
        (root / 'package-lock.json').write_text(json.dumps({'lockfileVersion': 3, 'packages': lock}))

    def run_notices(self, root):
        output = root / 'NOTICES'
        result = subprocess.run(['node', str(NPM_NOTICES), str(root), str(output)], capture_output=True, text=True)
        return result, output.read_text() if output.is_file() else None

    def test_notices_list_installed_production_packages_with_their_licence_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.project(root, {
                'node_modules/b': ({'version': '2.0.0'}, {'name': 'b', 'version': '2.0.0', 'license': '(MIT OR GPL-3.0-or-later)'},
                                   {'LICENSE.md': 'b licence\r\n', 'index.js': ''}),
                'node_modules/a': ({'version': '1.0.0'}, {'name': 'a', 'version': '1.0.0', 'license': 'Apache-2.0'}, {}),
                'node_modules/a/node_modules/b': ({'version': '2.0.0'}, {'name': 'b', 'version': '2.0.0', 'license': '(MIT OR GPL-3.0-or-later)'},
                                                  {'LICENSE.md': 'b licence\n'}),
                'node_modules/legacy': ({'version': '0.1.0'}, {'name': 'legacy', 'version': '0.1.0', 'licenses': [{'type': 'MIT'}]},
                                        {'LICENSE': 'legacy', 'NOTICE': 'legacy notice'}),
                'node_modules/dev': ({'version': '1.0.0', 'dev': True}, {'name': 'dev', 'version': '1.0.0', 'license': 'GPL-3.0'}, {}),
                'node_modules/other-platform': ({'version': '1.0.0', 'optional': True}, None, {}),
                'node_modules/linked': ({'resolved': '../linked', 'link': True}, None, {}),
            })
            result, text = self.run_notices(root)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('bundled into @papergo/app', text)
        self.assertIn('dependency, 3 packages,', text)
        self.assertIn('  a 1.0.0 (Apache-2.0)\n  b 2.0.0 ((MIT OR GPL-3.0-or-later))\n  legacy 0.1.0 (MIT)\n', text)
        self.assertIn('a 1.0.0\nLicence: Apache-2.0\n\nThe package ships no licence file; its package.json declares Apache-2.0.\n', text)
        self.assertIn('b 2.0.0\nLicence: (MIT OR GPL-3.0-or-later)\n\n--- LICENSE.md ---\n\nb licence\n', text)
        self.assertIn('--- LICENSE ---\n\nlegacy\n\n--- NOTICE ---\n\nlegacy notice\n', text)
        self.assertNotIn('dev 1.0.0', text)
        self.assertNotIn('other-platform', text)

    def test_notices_refuse_disallowed_unreviewed_and_missing_packages(self):
        for name, entry, manifest, message in [
                ('gpl', {'version': '1.0.0'}, {'license': 'GPL-3.0'}, 'gpl is licensed GPL-3.0, which docs/dependencies.md does not allow'),
                ('none', {'version': '1.0.0'}, {}, 'none declares no licence'),
                ('isbot', {'version': '1.0.0'}, {'license': 'CC0-1.0'}, 'isbot is now licensed CC0-1.0, not the reviewed Unlicense'),
                ('absent', {'version': '1.0.0'}, None, 'node_modules/absent is not installed')]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                if manifest is not None:
                    manifest = {'name': name, 'version': '1.0.0', **manifest}
                self.project(root, {f'node_modules/{name}': (entry, manifest, {})})
                result, text = self.run_notices(root)
                self.assertEqual(result.returncode, 1)
                self.assertIn(message, result.stderr)
                self.assertIsNone(text)
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.project(root, {'node_modules/isbot': ({'version': '5.2.2'}, {'name': 'isbot', 'version': '5.2.2', 'license': 'Unlicense'},
                                                       {'LICENSE': 'This is free and unencumbered software'})})
            result, text = self.run_notices(root)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('isbot 5.2.2\nLicence: Unlicense\n', text)


if __name__ == '__main__':
    unittest.main()
