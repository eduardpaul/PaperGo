#!/usr/bin/env python3
"""Compare the current storage workload with a disposable checkout of a Git ref.

Requires the pinned SDKs from scripts/native.py. Both binaries run the identical
benchmark source, serially in alternating order, on new databases each time.
No deployment database is opened. Build time is excluded from workload timings.
"""

import argparse
from datetime import datetime, timezone
import io
import json
import os
from pathlib import Path
import platform
import re
import statistics
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parent.parent
BENCHMARK = Path('internal/runner/storage_benchmark_test.go')


def run(command, cwd=ROOT, **kwargs):
    try:
        return subprocess.check_output(command, cwd=cwd, text=True, **kwargs).strip()
    except subprocess.CalledProcessError as error:
        print(error.output, flush=True)
        raise


def parse_metrics(output):
    samples = {}
    for line in output.splitlines():
        match = re.match(r'^BenchmarkStorageEfficiency/(definition-\d+)-\d+\s+1\s+(.*)$', line)
        if not match:
            continue
        values = match[2].split()
        samples[match[1]] = {values[i + 1]: float(values[i]) for i in range(0, len(values), 2)}
    if set(samples) != {'definition-0', 'definition-8192'}:
        raise ValueError(f'benchmark did not produce both workloads:\n{output}')
    return samples


def markdown(report):
    lines = [
        '# Storage efficiency comparison', '',
        f"Measured {report['measured_at']} on {report['platform']} with {report['go_version']}.", '',
        f"Baseline: `{report['base_ref']}` (`{report['base_commit']}`). "
        f"Current: `{report['current_commit']}` with the working-tree fixes and benchmark.", '',
        f"Medians of {report['repeats']} fresh databases per revision and workload. "
        'The same benchmark source runs in both revisions; execution order alternates each round. '
        'Compilation and fixture setup are excluded from workload timing. '
        'SQLite WAL and FULL synchronous durability are enabled in both revisions.', '',
        'Each workload creates 100 items and executes 100 five-step application workflows with '
        'default search automation disabled. The definition starts with either an empty or 8 KiB '
        'variable, which the first step clears so later outputs do not repeat that variable. '
        'Run details are read once per run. Storage is sampled after a completed passive checkpoint, '
        'without VACUUM. The journal measurement is its retained physical high water.', '',
        'Then 180 real maintenance ticks execute. Only their completion timestamps are moved '
        'three hours into the past before invoking the ordinary retention pass. '
        'All 100 application runs must survive that pass. This checks retention eligibility; '
        'it is not a multi-hour idle soak.', '',
        '| Workload | Metric | origin/main | Current | Change |',
        '| --- | --- | ---: | ---: | ---: |',
    ]
    metrics = [
        ('database-bytes', 'Main database', 1024, 'KiB'),
        ('database-growth-bytes', 'Main database growth during workload', 1024, 'KiB'),
        ('journal-bytes', 'Retained WAL', 1024, 'KiB'),
        ('step-allocation-bytes', 'Step tables + primary-key indexes', 1024, 'KiB'),
        ('marker-bytes', 'Transactional output payloads', 1024, 'KiB'),
        ('dbos-step-bytes', 'DBOS step output payloads', 1024, 'KiB'),
        ('workload-ns/item', 'Create + complete workflow per item', 1e6, 'ms'),
        ('details-ns/run', 'Run details per run', 1e6, 'ms'),
        ('purge-ns', 'Retention pass', 1e6, 'ms'),
        ('maintenance-remaining', 'Maintenance executions after retention', 1, 'rows'),
    ]
    for workload, measurements in report['medians'].items():
        for key, label, scale, unit in metrics:
            before, after = measurements['baseline'][key], measurements['current'][key]
            delta = f'{(after / before - 1) * 100:+.1f}%' if before else 'n/a'
            lines.append(f'| {workload} | {label} | {before / scale:,.2f} {unit} | '
                         f'{after / scale:,.2f} {unit} | {delta} |')
    lines += ['', 'Negative changes mean fewer bytes/rows or less elapsed time. '
              'Retention timing compares different amounts of work: the current revision deletes '
              'expired maintenance executions, while the baseline retains them. '
              'Timing results are local measurements, not throughput guarantees; filesystem caching, '
              'concurrent host activity and the workload shape affect them. '
              'Uploaded file bytes, external AI providers, OCR and native vector search are outside this workload.', '',
              'Reproduce:', '', '```bash',
              'python3 scripts/native.py',
              f"python3 scripts/compare-storage.py --base {report['base_commit']} --repeats {report['repeats']}",
              '```', '']
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='origin/main')
    parser.add_argument('--repeats', type=int, default=3)
    parser.add_argument('--output', type=Path, default=ROOT / 'reports/storage-efficiency-comparison')
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error('--repeats must be positive')
    native = ROOT / '.tools/native/native.env'
    if not native.is_file():
        parser.error('run python3 scripts/native.py first')
    env = dict(os.environ, PAPERGO_TEST_SEARCH_STORE='sqlite')
    env.update(line.split('=', 1) for line in native.read_text().splitlines())
    base = run(['git', 'rev-parse', args.base])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    samples = {'baseline': [], 'current': []}
    with tempfile.TemporaryDirectory(prefix='papergo-storage-') as temporary:
        tmp = Path(temporary)
        baseline = tmp / 'baseline'
        baseline.mkdir()
        archive = subprocess.check_output(['git', 'archive', base], cwd=ROOT)
        with tarfile.open(fileobj=io.BytesIO(archive)) as source:
            source.extractall(baseline, filter='data')
        (baseline / BENCHMARK).write_bytes((ROOT / BENCHMARK).read_bytes())
        binaries = {}
        for label, directory in [('baseline', baseline), ('current', ROOT)]:
            binary = tmp / f'{label}.test'
            print(f'Building {label} runner benchmark...', flush=True)
            subprocess.run(['go', 'test', '-c', '-o', str(binary), './internal/runner'],
                           cwd=directory, env=env, check=True)
            binaries[label] = binary
        for repetition in range(args.repeats):
            order = ['baseline', 'current'] if repetition % 2 == 0 else ['current', 'baseline']
            for label in order:
                print(f'Round {repetition + 1}/{args.repeats}: {label}', flush=True)
                output = run([str(binaries[label]), '-test.run=^$',
                              '-test.bench=^BenchmarkStorageEfficiency$', '-test.benchtime=1x',
                              '-test.timeout=5m'], env=env)
                print(output, flush=True)
                samples[label].append(parse_metrics(output))
    medians = {}
    for workload in samples['baseline'][0]:
        medians[workload] = {
            label: {metric: statistics.median(sample[workload][metric] for sample in values)
                    for metric in values[0][workload]}
            for label, values in samples.items()
        }
    report = {'measured_at': datetime.now(timezone.utc).isoformat(), 'platform': platform.platform(),
              'go_version': run(['go', 'version']), 'base_ref': args.base, 'base_commit': base,
              'current_commit': run(['git', 'rev-parse', 'HEAD']),
              'working_tree_status': run(['git', 'status', '--short']),
              'repeats': args.repeats, 'samples': samples, 'medians': medians}
    args.output.with_suffix('.json').write_text(json.dumps(report, indent=2) + '\n')
    args.output.with_suffix('.md').write_text(markdown(report))
    print(f"Reports: {args.output}.md and {args.output}.json", flush=True)


if __name__ == '__main__':
    main()
