#!/usr/bin/env python3
"""Run the packaged Windows executable on a Windows without the Visual C++
runtime: a Windows Server Core container, process-isolated on a Windows host
of the same build, which ships no vcruntime140.dll or msvcp140.dll. The
executable must extract its embedded ONNX Runtime and read text with OCR.

Requires Docker with Windows containers. The host drives the API through
the container's address.
"""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import time
import urllib.error
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
TOKEN = 'papergo-clean-windows-development-token'
IMAGE = 'mcr.microsoft.com/windows/servercore:ltsc2025'
CONTAINER = 'papergo-clean-windows'
# Rendered by PDFium, previewed as a JPEG without a text layer, then read
# back from that image by OCR.
WORDS = ['Clean', 'Windows', 'OCR']


def docker(*arguments, check=True):
    print('+ docker ' + ' '.join(arguments), flush=True)
    result = subprocess.run(['docker', *arguments], capture_output=True, text=True)
    output = (result.stdout + result.stderr).strip()
    if output:
        print(output, flush=True)
    if check and result.returncode:
        raise RuntimeError(f'docker {arguments[0]} failed with exit code {result.returncode}')
    return result.stdout


def powershell(command):
    return docker('exec', CONTAINER, 'powershell', '-NoProfile', '-NonInteractive', '-Command', command)


def request(base, path, data=None, raw=None, content_type=None):
    headers = {'Authorization': f'Bearer {TOKEN}'}
    if data is not None:
        raw, content_type = json.dumps(data).encode(), 'application/json'
    if content_type:
        headers['Content-Type'] = content_type
    try:
        with urllib.request.urlopen(urllib.request.Request(base + path, data=raw, headers=headers), timeout=30) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        error.add_note(f'{path}: {error.read().decode()}')
        raise


def upload(base, library, name, data):
    boundary = 'papergo-clean-windows'
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
            'Content-Type: application/octet-stream\r\n\r\n').encode() + data + f'\r\n--{boundary}--\r\n'.encode()
    return json.loads(request(base, f'/v1/resources/{library}/documents', raw=body,
                              content_type=f'multipart/form-data; boundary={boundary}'))['item']['id']


def text_pdf(text):
    pdf, offsets = bytearray(b'%PDF-1.4\n'), []
    stream = f'BT /F1 40 Tf 60 600 Td ({text}) Tj ET'
    for body in ['<</Type/Catalog/Pages 2 0 R>>', '<</Type/Pages/Kids[4 0 R]/Count 1>>',
                 '<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>',
                 '<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 5 0 R/Resources<</Font<</F1 3 0 R>>>>>>',
                 f'<</Length {len(stream)}>>\nstream\n{stream}\nendstream']:
        offsets.append(len(pdf))
        pdf.extend(f'{len(offsets)} 0 obj\n{body}\nendobj\n'.encode())
    xref = len(pdf)
    pdf.extend(f'xref\n0 {len(offsets) + 1}\n0000000000 65535 f \n'.encode())
    for offset in offsets:
        pdf.extend(f'{offset:010d} 00000 n \n'.encode())
    pdf.extend(f'trailer\n<</Size {len(offsets) + 1}/Root 1 0 R>>\nstartxref\n{xref}\n%%EOF\n'.encode())
    return bytes(pdf)


def wait(description, attempt, seconds=120):
    deadline = time.monotonic() + seconds
    while True:
        result = attempt()
        if result:
            return result
        if time.monotonic() > deadline:
            raise RuntimeError(f'{description} did not complete')
        time.sleep(1)


def exercise(base):
    workspace = json.loads(request(base, '/v1/workspaces', {'name': 'Clean Windows'}))['id']
    library = json.loads(request(base, f'/v1/resources/{workspace}/children', {'kind': 'library', 'name': 'Scans'}))['id']
    pdf = upload(base, library, 'page.pdf', text_pdf(' '.join(WORDS)))

    def preview():
        data = request(base, f'/v1/items/{pdf}/preview')
        if b'unavailable' in data[:200]:
            raise RuntimeError('the PDF preview could not be rendered')
        return data if data[:3] == b'\xff\xd8\xff' else None
    image = upload(base, library, 'page.jpg', wait('the PDF preview', preview))
    operation = json.loads(request(base, f'/v1/items/{image}/text', {'ocr': 'always'}))

    def read():
        state = json.loads(request(base, f'/v1/operations/{operation["id"]}'))
        if state['status'] not in ('pending', 'succeeded'):
            raise RuntimeError(f'OCR failed: {state}')
        return state if state['status'] == 'succeeded' else None
    result = wait('OCR', read)['result']
    pages = json.loads(request(base, f'/v1/items/{image}/text'))
    print('OCR result:', json.dumps(result), flush=True)
    print('OCR text:', json.dumps(pages), flush=True)
    text = json.dumps(pages).lower()
    if result.get('ocr_pages') != 1 or not all(word.lower() in text for word in WORDS):
        raise RuntimeError(f'OCR did not read {WORDS} from the image')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, help='the Windows platform ZIP (default: the one in dist/)')
    parser.add_argument('--assets', type=Path, help='the shared assets ZIP (default: the one in dist/)')
    parser.add_argument('--image', default=IMAGE)
    args = parser.parse_args()
    archives = [args.archive] if args.archive else sorted((ROOT / 'dist').glob('papergo_*_windows_amd64.zip'))
    assets = [args.assets] if args.assets else sorted((ROOT / 'dist').glob('papergo_*_assets.zip'))
    if len(archives) != 1 or len(assets) != 1:
        raise ValueError('Expected one Windows platform ZIP and one shared assets ZIP')
    work = ROOT / '.cache/clean-windows'
    shutil.rmtree(work, ignore_errors=True)
    package, data = work / 'papergo', work / 'data'
    data.mkdir(parents=True)
    for archive in (archives[0], assets[0]):
        with zipfile.ZipFile(archive) as source:
            source.extractall(package)
    dlls = sorted(path.name for path in package.rglob('*.dll'))
    if dlls:
        raise RuntimeError(f'the package ships DLLs: {dlls}')
    executable = package / 'papergo.exe'
    print(f'papergo.exe: {executable.stat().st_size} bytes', flush=True)
    if shutil.which('objdump'):
        imports = subprocess.run(['objdump', '-p', str(executable)], check=True, capture_output=True, text=True).stdout
        print('papergo.exe imports:', ', '.join(line.split(':', 1)[1].strip() for line in imports.splitlines()
                                                 if 'DLL Name:' in line), flush=True)
    docker('rm', '--force', CONTAINER, check=False)
    docker('run', '--detach', '--name', CONTAINER, '--isolation=process',
           '--volume', f'{package}:C:\\papergo:ro', '--volume', f'{data}:C:\\data', '--workdir', 'C:\\data',
           '--env', 'APP_ENV=test', '--env', 'AUTH_MODE=development', '--env', f'DEV_TOKEN={TOKEN}',
           '--env', 'ADMIN_SUBJECT=smoke-admin', '--env', 'DEV_SUBJECT=smoke-admin', '--env', 'HTTP_ADDR=0.0.0.0:8080',
           '--env', 'DATABASE_PATH=C:\\data\\papergo.db', '--env', 'BLOB_PATH=C:\\data\\blobs',
           args.image, 'C:\\papergo\\papergo.exe')
    try:
        # The image has no Visual C++ runtime for Windows to find. (.NET
        # Framework's private vcruntime140_clr0400.dll is another module.)
        powershell('Get-ChildItem C:\\Windows\\System32\\vcruntime140*.dll, C:\\Windows\\System32\\msvcp140*.dll '
                   '-ErrorAction SilentlyContinue | ForEach-Object FullName')
        for name in ('vcruntime140.dll', 'vcruntime140_1.dll', 'msvcp140.dll', 'msvcp140_1.dll'):
            present = powershell(f'Test-Path C:\\Windows\\System32\\{name}').strip()
            print(f'C:\\Windows\\System32\\{name} present: {present}', flush=True)
            if present != 'False':
                raise RuntimeError(f'the container has {name}; it does not test a Windows without the Visual C++ runtime')
        address = docker('inspect', '--format', '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}', CONTAINER).strip()
        base = f'http://{address}:8080'

        def ready():
            if docker('inspect', '--format', '{{.State.Running}}', CONTAINER).strip() != 'true':
                raise RuntimeError('papergo exited')
            try:
                return request(base, '/health/ready')
            except (OSError, urllib.error.URLError):
                return None
        wait('papergo startup', ready, seconds=180)
        exercise(base)
        # ONNX Runtime and the Visual C++ runtime load from the extracted cache.
        modules = powershell("(Get-Process papergo).Modules | Where-Object ModuleName -Match '^(onnxruntime|vcruntime140|msvcp140)' "
                             "| ForEach-Object FileName")
        extracted = [line for line in modules.splitlines() if line.strip()]
        if len(extracted) < 5 or not all(line.lower().startswith('c:\\data\\cache\\onnxruntime\\') for line in extracted):
            raise RuntimeError(f'the runtime did not load from C:\\data\\cache\\onnxruntime: {extracted}')
        powershell('Get-ChildItem -Recurse C:\\data\\cache\\onnxruntime | Format-Table -AutoSize Mode, Length, FullName')
    finally:
        docker('logs', CONTAINER, check=False)
        docker('rm', '--force', CONTAINER, check=False)
    print('A Windows without the Visual C++ runtime ran papergo.exe: it extracted its ONNX Runtime and read text with OCR.')


if __name__ == '__main__':
    main()
