#!/usr/bin/env python3
"""Print the toolchain versions the Dockerfile pins, as name=value lines for
$GITHUB_OUTPUT, so CI jobs that cannot run in the toolchain image install
the same Go, Node and CMake."""
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent
NAMES = {'GO_VERSION': 'go', 'NODE_VERSION': 'node', 'CMAKE_VERSION': 'cmake'}


def versions(dockerfile=ROOT / 'Dockerfile'):
    found = dict(re.findall(r'^ARG (\w+)=(\S+)$', dockerfile.read_text(), re.MULTILINE))
    missing = [name for name in NAMES if name not in found]
    if missing:
        raise SystemExit(f'{dockerfile} does not pin {", ".join(missing)}')
    return {output: found[name] for name, output in NAMES.items()}


if __name__ == '__main__':
    for name, value in versions().items():
        print(f'{name}={value}')
