#!/usr/bin/env python3
"""Run packaged native executables against a fresh disposable database."""
from contextlib import closing
import argparse
import json
import os
from pathlib import Path
import socket
import platform
import shutil
import struct
import sqlite3
import subprocess
import tempfile
import zipfile
import time
import urllib.error
import urllib.request
import urllib.parse
import zlib

ROOT = Path(__file__).resolve().parent.parent
TOKEN = "papergo-release-smoke-development-token"
AUTHORIZATION = f'Bearer {TOKEN}'


def request(base, path, data=None, method=None):
    headers = {"Authorization": AUTHORIZATION}
    if data is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(data).encode()
    try:
        with urllib.request.urlopen(urllib.request.Request(base + path, data=data, headers=headers, method=method), timeout=10) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        error.add_note(f'{method or ("POST" if data is not None else "GET")} {path}: {error.read().decode()}')
        raise


def upload(base, library, name, data):
    boundary = 'papergo-release-smoke'
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
            'Content-Type: application/octet-stream\r\n\r\n').encode() + data + f'\r\n--{boundary}--\r\n'.encode()
    req = urllib.request.Request(base + f'/v1/resources/{library}/documents', data=body, headers={
        'Authorization': AUTHORIZATION, 'Content-Type': f'multipart/form-data; boundary={boundary}',
    })
    with urllib.request.urlopen(req, timeout=10) as response:
        return json.load(response)['item']['id']


def exercise_native(base, workspace):
    library = json.loads(request(base, f'/v1/resources/{workspace}/children', {'kind': 'library', 'name': 'Scans'}))['id']
    # PDFium and statically linked WebP must render a PDF preview.
    pdf, offsets = bytearray(b'%PDF-1.4\n'), []
    stream = 'BT /F1 14 Tf 40 700 Td (Standalone PDF) Tj ET'
    for body in ['<</Type/Catalog/Pages 2 0 R>>', '<</Type/Pages/Kids[4 0 R]/Count 1>>',
                 '<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>',
                 '<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 5 0 R/Resources<</Font<</F1 3 0 R>>>>>>',
                 f'<</Length {len(stream)}>>\nstream\n{stream}\nendstream']:
        offsets.append(len(pdf))
        pdf.extend(f'{len(offsets)} 0 obj\n{body}\nendobj\n'.encode())
    xref = len(pdf)
    pdf.extend(f'xref\n0 {len(offsets)+1}\n0000000000 65535 f \n'.encode())
    for offset in offsets:
        pdf.extend(f'{offset:010d} 00000 n \n'.encode())
    pdf.extend(f'trailer\n<</Size {len(offsets)+1}/Root 1 0 R>>\nstartxref\n{xref}\n%%EOF\n'.encode())
    item = upload(base, library, 'page.pdf', pdf)
    for _ in range(120):
        preview = request(base, f'/v1/items/{item}/preview')
        if preview[:4] == b'RIFF' and preview[8:12] == b'WEBP':
            break
        if b'unavailable' in preview:
            raise RuntimeError('packaged PDF renderer could not generate a preview')
        time.sleep(.25)
    else:
        raise RuntimeError('PDF preview did not complete')
    # Require real document and query vectors from the selected shared preset.
    for _ in range(240):
        status = json.loads(request(base, '/v1/search/status'))
        if (status.get('embeddings') or {}).get('vectors', 0) > 0:
            break
        time.sleep(.25)
    else:
        raise RuntimeError(f'shared embedding model could not index PDF text: {status}')
    for _ in range(240):
        result = json.loads(request(base, '/v1/search?' + urllib.parse.urlencode({
            'q': 'Standalone PDF', 'mode': 'semantic', 'workspace_id': workspace,
        })))
        assert result['mode'] == 'semantic', result
        if any(hit['id'] == item for hit in result.get('value', [])):
            break
        time.sleep(.25)
    else:
        raise RuntimeError(f'PDF text was not searchable with the selected preset: {result}')
    # Laya must execute an actual decision from the shared assets directory.
    flow = json.loads(request(base, f'/v1/workspaces/{workspace}/workflows', {
        'name': 'Offline decision', 'definition': {'triggers': [{'type': 'manual', 'collection_id': library}],
        'flow': {'start': 'decide', 'nodes': {'decide': {'activity': 'ai.decide', 'inputs': {
            'data': {'document': 'An invoice for office supplies.'},
            'questions': [{'key': 'kind', 'instructions': 'Which category describes this document?',
                           'options': [{'key': 'invoice', 'text': 'Invoice'}, {'key': 'other', 'text': 'Other document'}]}],
        }}}}},
    }))
    started = json.loads(request(base, f'/v1/workflows/{flow["id"]}/runs', {'item_ids': [item]}))
    for _ in range(240):
        run = json.loads(request(base, '/v1/workflow-runs/' + started['run_ids'][0]))
        if run['status'] == 'completed':
            break
        if run['status'] == 'failed':
            raise RuntimeError(f'shared Laya failed: {run}')
        time.sleep(.25)
    else:
        raise RuntimeError(f'shared Laya workflow did not complete: {run}')
    # A blank PNG exercises ONNX Runtime and the shared PaddleOCR models.
    flow = json.loads(request(base, f'/v1/workspaces/{workspace}/workflow-builtins/documents.optimize',
                              {'collection_id': library, 'automatic_enabled': True}, method='PUT'))
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    png = (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 800, 600, 8, 0, 0, 0, 0)) +
           chunk(b'IDAT', zlib.compress((b'\x00' + b'\xff' * 800) * 600)) + chunk(b'IEND', b''))
    item = upload(base, library, 'blank.png', png)
    for _ in range(180):
        runs = json.loads(request(base, f'/v1/workspaces/{workspace}/workflow-runs?item_id={item}&workflow_id={flow["id"]}')).get('value') or []
        if runs and runs[0]['status'] == 'completed':
            break
        if runs and runs[0]['status'] == 'failed':
            raise RuntimeError(f'shared OCR failed: {runs[0]}')
        time.sleep(.25)
    else:
        raise RuntimeError('shared OCR workflow did not complete')


def exercise(binary, admin, directory, expected_version=None, model='embeddinggemma-2'):
    directory.mkdir(parents=True, exist_ok=True)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    env = dict(os.environ, APP_ENV="test", AUTH_MODE="development", DEV_TOKEN=TOKEN,
               ADMIN_SUBJECT="smoke-admin", DEV_SUBJECT="smoke-admin", HTTP_ADDR=f"127.0.0.1:{port}",
               DATABASE_PATH=str(directory / "papergo.db"), BLOB_PATH=str(directory / "blobs"),
               SEARCH_STORE='zvec', SEARCH_ZVEC_PATH=str(directory / 'zvec'),
               AI_EMBEDDINGS_PROVIDER='local', AI_EMBEDDINGS_MODEL=model)
    for name in ('ONNXRUNTIME_LIB', 'OCR_MODELS', 'AI_DECISION_MODELS_PATH', 'AI_EMBEDDINGS_MODELS_PATH',
                 'PAPERGO_CONFIG', 'LD_LIBRARY_PATH', 'DYLD_LIBRARY_PATH'):
        env.pop(name, None)
    excluded = {os.path.normcase(str(ROOT / '.tools/native/lib'))}
    compiler = Path(os.environ.get('CC', ''))
    if compiler.is_absolute():
        excluded.add(os.path.normcase(str(compiler.parent)))
    env['PATH'] = os.pathsep.join(part for part in env['PATH'].split(os.pathsep) if os.path.normcase(part) not in excluded)
    assert 'PaperGo' in subprocess.check_output([str(binary), '--version'], cwd=directory, env=env, text=True)
    with (directory / "server.log").open("w+") as log:
        process = subprocess.Popen([str(binary)], cwd=directory, env=env, stdout=log, stderr=log)
        try:
            for attempt in range(90):
                if process.poll() is not None:
                    break
                try:
                    request(base, "/health/ready")
                    break
                except (OSError, urllib.error.URLError):
                    time.sleep(1)
            else:
                raise RuntimeError("release server did not become ready")
            if process.poll() is not None:
                raise RuntimeError("release server exited")
            assert b"<html" in request(base, "/").lower()
            request(base, "/health/live")
            # Exercise writes after migration; SQLite functions, triggers and
            # foreign-key enforcement must work in the released executable.
            workspace = json.loads(request(base, "/v1/workspaces", {"name": "Release smoke"}))
            assert workspace.get("id"), workspace
            exercise_native(base, workspace['id'])
        except Exception:
            log.flush()
            print((directory / "server.log").read_text())
            raise
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
    if admin:
        subprocess.run([str(admin), "backup", "--search-store", "zvec", "--file", str(directory / "backup.tar.gz")],
                       cwd=directory, env=env, check=True)
    with closing(sqlite3.connect(directory / "papergo.db")) as db:
        if expected_version:
            assert db.execute("SELECT hash FROM schema_metadata").fetchone()[0] == expected_version
        assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
        assert not db.execute("PRAGMA foreign_key_check").fetchall()


def extract_zip(path, directory):
    with zipfile.ZipFile(path) as archive:
        archive.extractall(directory)
        if os.name != 'nt':
            for entry in archive.infolist():
                mode = (entry.external_attr >> 16) & 0o777
                if mode:
                    (directory / entry.filename).chmod(mode)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group()
    source.add_argument('--bin-dir', type=Path, help='Smoke test a local build directory including assets')
    source.add_argument('--archive', type=Path, help='Smoke test a specific platform ZIP')
    parser.add_argument('--assets', type=Path, help='Shared assets ZIP to extract beside the executables')
    args = parser.parse_args()
    suffix = '.exe' if os.name == 'nt' else ''
    with tempfile.TemporaryDirectory(prefix='papergo-release-package-') as temporary:
        directory = Path(temporary)
        package = directory / 'application with spaces'
        if args.bin_dir:
            shutil.copytree(args.bin_dir, package)
            current = None
        else:
            if not args.assets:
                parser.error('--assets is required when testing a platform ZIP')
            system = platform.system().lower()
            arch = 'arm64' if platform.machine().lower() in ('aarch64', 'arm64') else 'amd64'
            archives = [args.archive] if args.archive else sorted((ROOT / 'dist').glob(f'papergo_*_{system}_{arch}.zip'))
            if len(archives) != 1:
                raise ValueError('Expected one packaged platform ZIP')
            extract_zip(archives[0], package)
            assert not (package / 'assets').exists(), 'Models must ship in the shared assets ZIP'
            bundle = json.loads((package / 'release-database.json').read_text())
            current = bundle['releases'][-1]
        if args.assets:
            extract_zip(args.assets, package)
        assert (package / 'assets/models/paddleocr').is_dir()
        assert (package / 'assets/models/laya').is_dir()
        for preset in ('embeddinggemma-2', 'harrier-oss-v1-0.6b'):
            assert (package / 'assets/models/embedding' / preset).is_dir()
        binary, admin = package / f'papergo{suffix}', package / f'papergo-admin{suffix}'
        for executable in (binary, admin, package / f'papergo-mcp{suffix}'):
            version = subprocess.check_output([str(executable), '--version'], cwd=directory, text=True)
            assert (current['version'] if current else 'PaperGo') in version
        # Use an unrelated working directory: native libraries and shared models
        # must be found beside the executable without development loader paths.
        expected = current['schema_hash'] if current else None
        exercise(binary, admin, directory / 'fresh', expected)
        exercise(binary, None, directory / 'harrier', expected, model='harrier-oss-v1-0.6b')
    print('Platform binaries plus shared assets: versions, UI, zvec, PDF previews, PaddleOCR, Laya, both embedding presets and backup passed.')


if __name__ == '__main__':
    main()
