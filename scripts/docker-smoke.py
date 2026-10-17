#!/usr/bin/env python3
"""Exercise a built image with production auth, immutable code and persisted data."""
import argparse
import base64
import importlib.util
import json
from pathlib import Path
import subprocess
import time
import urllib.error

spec = importlib.util.spec_from_file_location('release_smoke', Path(__file__).with_name('release-smoke.py'))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def docker(*args):
    return subprocess.check_output(['docker', *args], text=True).strip()


def ready(container):
    port = docker('port', container, '8080/tcp')
    base = 'http://' + port
    for _ in range(120):
        if docker('inspect', '--format={{.State.Running}}', container) != 'true':
            break
        try:
            smoke.request(base, '/health/ready')
            return base
        except (OSError, urllib.error.URLError):
            time.sleep(1)
    raise RuntimeError('Image did not become ready')


def exercise(image, version):
    password = 'docker-smoke-initial-password'
    smoke.AUTHORIZATION = 'Basic ' + base64.b64encode(f'admin:{password}'.encode()).decode()
    volume = docker('volume', 'create')
    container = None
    try:
        config = json.loads(docker('image', 'inspect', image))[0]['Config']
        assert config['User'] == '65532:65532', config['User']
        assert config['Healthcheck']['Test'] == ['CMD', '/opt/papergo/papergo', '--health']
        for program in ('/opt/papergo/papergo', '/opt/papergo/papergo-admin'):
            output = docker('run', '--rm', '--network=none', '--entrypoint', program, image, '--version')
            assert 'PaperGo' in output and (not version or version in output), output
        # A fresh production instance must not expose a default admin password.
        container = docker('create', '--network=none', '--volume', volume + ':/data', image)
        docker('start', container)
        for _ in range(60):
            if docker('inspect', '--format={{.State.Running}}', container) == 'false':
                break
            time.sleep(.5)
        else:
            raise RuntimeError('Unconfigured production image accepted the default admin password')
        assert 'admin password is required' in docker('logs', container)
        docker('rm', container)
        container = None
        for model in ('embeddinggemma-2', 'harrier-oss-v1-0.6b'):
            container = docker('create', '--publish', '127.0.0.1::8080', '--volume', volume + ':/data',
                               '--read-only', '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m,mode=1777',
                               '--cap-drop=ALL', '--security-opt=no-new-privileges:true',
                               '--env', 'ADMIN_PASSWORD=' + password,
                               '--env', 'AI_EMBEDDINGS_PROVIDER=local', '--env', 'AI_EMBEDDINGS_MODEL=' + model, image)
            docker('start', container)
            base = ready(container)
            assert b'<html' in smoke.request(base, '/').lower()
            # A second start uses the same named data volume and existing admin.
            if model == 'harrier-oss-v1-0.6b':
                workspaces = json.loads(smoke.request(base, '/v1/workspaces')).get('value') or []
                assert any(item['id'] == previous_workspace for item in workspaces), workspaces
            workspace = json.loads(smoke.request(base, '/v1/workspaces', {'name': model}))
            previous_workspace = workspace['id']
            smoke.exercise_native(base, workspace['id'])
            docker('exec', container, '/opt/papergo/papergo', '--health')
            docker('stop', '--time=45', container)
            assert docker('inspect', '--format={{.State.ExitCode}}', container) == '0'
            docker('rm', container)
            container = None
        print('Image version, production authentication, persistence, graceful stop, UI, zvec, PDF, OCR, Laya and both embedding presets passed.')
    finally:
        if container:
            subprocess.run(['docker', 'logs', '--tail=100', container])
            subprocess.run(['docker', 'rm', '--force', container], check=True, stdout=subprocess.DEVNULL)
        subprocess.run(['docker', 'volume', 'rm', volume], check=True, stdout=subprocess.DEVNULL)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--version')
    args = parser.parse_args()
    exercise(args.image, args.version)
