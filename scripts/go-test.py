#!/usr/bin/env python3
"""Stream Go test results and save JSON events, timings and a Markdown summary."""

import argparse
from collections import defaultdict
import json
import os
from pathlib import Path
import subprocess
import sys
import time


def summarize(events, wall_seconds, preparation_seconds, exit_code):
    packages, tests = [], []
    cached = {event.get('Package') for event in events if '(cached)' in event.get('Output', '') and not event.get('Test')}
    for event in events:
        if event.get('Action') not in {'pass', 'fail', 'skip'}:
            continue
        row = {key: event[key] for key in ('Package', 'Test', 'Action', 'Elapsed') if key in event}
        if 'Test' not in row:
            row['Cached'] = row['Package'] in cached
            packages.append(row)
        elif '/' not in row['Test']:
            tests.append(row)
    return {'exit_code': exit_code, 'wall_seconds': round(wall_seconds, 3),
            'preparation_seconds': round(preparation_seconds, 3),
            'build_failures': [event['ImportPath'] for event in events if event.get('Action') == 'build-fail'],
            'packages': packages, 'tests': sorted(tests, key=lambda row: row.get('Elapsed', 0), reverse=True)}


def markdown(report):
    lines = ['## Go test timings', '', f"Command wall time: {report['wall_seconds']:.2f}s; "
             f"preparation/discovery: {report['preparation_seconds']:.2f}s; exit code: {report['exit_code']}.", '',
             'Package elapsed times can overlap. Command wall time includes build/link and scheduling; '
             'cached Go results retain their original elapsed values.', '',
             '| Package | Result | Seconds |', '| --- | --- | ---: |']
    for row in report['packages']:
        result = row['Action'] + (' (cached)' if row['Cached'] else '')
        lines.append(f"| {row['Package']} | {result} | {row.get('Elapsed', 0):.2f} |")
    if report['build_failures']:
        lines += ['', 'Failed builds: ' + ', '.join(report['build_failures']) + '.']
    lines += ['', 'Slowest top-level tests (subtests are included in their parent):', '',
              '| Test | Package | Result | Seconds |', '| --- | --- | --- | ---: |']
    for row in report['tests'][:20]:
        lines.append(f"| {row['Test']} | {row['Package']} | {row['Action']} | {row.get('Elapsed', 0):.2f} |")
    return '\n'.join(lines) + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='path stem for .jsonl, .json and .md reports')
    parser.add_argument('--preparation-seconds', type=float, default=0)
    parser.add_argument('flags', nargs=argparse.REMAINDER, help='go test arguments after --')
    args = parser.parse_args()
    flags = args.flags[1:] if args.flags[:1] == ['--'] else args.flags
    if not flags:
        parser.error('provide go test arguments after --')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    events, output = [], defaultdict(list)
    started = time.monotonic()
    with args.output.with_suffix('.jsonl').open('w', encoding='utf-8') as raw:
        process = subprocess.Popen(['go', 'test', '-json', *flags], stdout=subprocess.PIPE, text=True, encoding='utf-8', errors='replace')
        try:
            for line in process.stdout:
                raw.write(line)
                raw.flush()
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    print(line, end='', flush=True)
                    continue
                events.append(event)
                package = event.get('Package', '')
                if event.get('Action') == 'build-output':
                    print(event['Output'], end='', flush=True)
                elif event.get('Action') == 'output':
                    output[package].append(event['Output'])
                    if not event.get('Test'):
                        print(event['Output'], end='', flush=True)
                if event.get('Action') == 'fail' and not event.get('Test'):
                    print(''.join(output[package]), file=sys.stderr, end='', flush=True)
                if event.get('Action') in {'pass', 'fail', 'skip'} and not event.get('Test'):
                    output.pop(package, None)
        finally:
            process.stdout.close()
            exit_code = process.wait()
            report = summarize(events, time.monotonic() - started, args.preparation_seconds, exit_code)
            report['command'] = ['go', 'test', '-json', *flags]
            report['go_version'] = subprocess.check_output(['go', 'version'], text=True).strip()
            native = Path('.tools/native/installed.json')
            report['native'] = json.loads(native.read_text()) if native.is_file() else None
            args.output.with_suffix('.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
            rendered = markdown(report)
            args.output.with_suffix('.md').write_text(rendered, encoding='utf-8')
            if os.environ.get('GITHUB_STEP_SUMMARY'):
                with open(os.environ['GITHUB_STEP_SUMMARY'], 'a', encoding='utf-8') as summary:
                    summary.write(rendered + '\n')
    print(f"Go tests: {report['wall_seconds']:.2f}s; reports: {args.output}.*", flush=True)
    return exit_code


if __name__ == '__main__':
    sys.exit(main())
