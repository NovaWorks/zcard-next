"""P4 native/container recovery, using ONLY an isolated stopped HTTP fixture.
Usage: python3 scripts/test_plugin_recovery.py /absolute/fixture [--docker-image zcard:p4-acceptance]
The input must be produced by test_plugin_p4.cjs; all operations use temporary copies.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('fixture', type=Path)
parser.add_argument('--docker-image')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
original = args.fixture.absolute()
assert (original / 'p4-state.json').is_file(), 'P4 stopped fixture required'
with socket.socket() as probe:
    probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    probe.bind(('127.0.0.1', 18083))
backup = Path(tempfile.mkdtemp(dir='/tmp', prefix='zcard-p4-backup-')) / 'instance'
shutil.copytree(original, backup)
restored = Path(tempfile.mkdtemp(dir='/tmp', prefix='zcard-p4-restore-')) / 'instance'
shutil.copytree(backup, restored)
config = restored / 'conf/config.yaml'
config.write_text(config.read_text().replace(str(original), str(restored)))
shutil.copy2(repo / 'server/bin/zcard', restored / 'zcard')

def verify(mode):
    for _ in range(150):
        try:
            urllib.request.urlopen('http://127.0.0.1:18083/health', timeout=1)
            break
        except OSError:
            time.sleep(.1)
    else:
        raise RuntimeError('health timeout')
    subprocess.run(['node', 'scripts/test_plugin_p4.cjs'], cwd=repo, env=dict(os.environ,
        ZCARD_P4_VERIFY=mode, ZCARD_TEST_BASE_URL='http://127.0.0.1:18083', ZCARD_TEST_FIXTURE=str(restored)), check=True)

def native(mode):
    with (restored / 'recovery.log').open('a') as log:
        process = subprocess.Popen([str(restored / 'zcard'), 'serve', '-conf', str(restored / 'conf')], cwd=restored, stdout=log, stderr=log)
        try:
            verify(mode)
            result = subprocess.run([str(restored / 'zcard'), 'plugin-host-check', '-conf', str(restored / 'conf')], cwd=restored)
            assert (result.returncode == 0) == (mode != 'missing')
        finally:
            process.terminate()
            process.wait(timeout=15)

native('restored')
native('restart')
state = json.loads((restored / 'p4-state.json').read_text())
artifact = restored / 'data/plugins/member-purchase-gate' / state['digest'] / 'archive.zip'
artifact.rename(artifact.with_suffix('.missing'))
native('missing')
artifact.with_suffix('.missing').rename(artifact)
print('PASS native backup / restore / restart / missing artifact:', restored, flush=True)

if args.docker_image:
    # Same final distroless runtime as deploy/Dockerfile; no Node or shell.
    # Use a Docker volume as in production: macOS bind mounts do not support
    # chmod on Unix sockets. Preserve fixture ownership for a non-root process.
    config.write_text(config.read_text().replace(str(restored), '/app').replace('127.0.0.1:18083', '0.0.0.0:18083'))
    name = 'zcard-p4-' + str(os.getpid())
    volume = name + '-data'
    subprocess.run(['docker', 'volume', 'create', volume], check=True, stdout=subprocess.DEVNULL)
    mount = ['--mount', f'type=volume,source={volume},target=/app']
    prep = name + '-prepare'
    subprocess.run(['docker', 'create', '--name', prep, *mount, args.docker_image, 'version'], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'cp', '-a', str(restored) + '/.', prep + ':/app'], check=True)
    subprocess.run(['docker', 'rm', prep], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'run', '--rm', '--user', '0', *mount, '--entrypoint', 'chown', 'mysql:8', '-R', '65532:65532', '/app'], check=True)
    docker = ['docker', 'run', '--rm', '--user', '65532:65532', *mount]
    subprocess.run([*docker, args.docker_image, 'plugin-host-check', '-conf', '/app/conf'], check=True)
    for attempt in range(2):
        subprocess.run(['docker', 'run', '-d', '--name', name, '--user', '65532:65532', *mount, '-p', '127.0.0.1:18083:18083', args.docker_image], check=True, stdout=subprocess.DEVNULL)
        try:
            verify('docker-recreate')
            no_node = subprocess.run(['docker', 'exec', name, 'node', '--version'], capture_output=True)
            assert no_node.returncode != 0, 'runtime unexpectedly contains Node'
            # Reinstall an already signed compatible package through the live
            # socket. This executes the actual container binary without Node.
            d = json.loads((restored / 'v2/descriptor.json').read_text())
            generation = '6' if attempt == 0 else '7'
            subprocess.run(['docker', 'exec', name, '/zcard', 'plugin', 'import', '--conf', '/app/conf', '--id', 'member-purchase-gate', '--action', 'upgrade', '--expected-generation', generation,
                '--digest', d['archiveSHA256'], '--descriptor', '/app/v2/descriptor.json', '--signature', '/app/v2/signature.ed25519', '--archive', '/app/v2/plugin.zplug', '--approve-scopes'], check=True)
            verify('docker-upgrade')
        finally:
            with (restored / f'docker-{attempt}.log').open('w') as log:
                subprocess.run(['docker', 'logs', name], stdout=log, stderr=log)
            subprocess.run(['docker', 'rm', '-f', name], check=True, stdout=subprocess.DEVNULL)
    # A bounded tmpfs produces an actual ENOSPC write, not a mock exception.
    manifest = json.loads((restored / 'manifest2.json').read_text())
    manifest['version'] = '0.1.2'
    (restored / 'full-manifest.json').write_text(json.dumps(manifest))
    source = (repo / 'examples/plugins/member-purchase-gate/main.js').read_text()
    (restored / 'full.js').write_text(source + '\n/*' + base64.b64encode(os.urandom(100000)).decode() + '*/')
    subprocess.run(['go', 'run', './tools/plugin-pack', '--manifest', str(restored / 'full-manifest.json'), '--script', str(restored / 'full.js'), '--key', str(restored / 'key.pem'), '--key-id', 'ui-test', '--out', str(restored / 'full')], cwd=repo / 'server', check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'run', '-d', '--name', name, '--user', '0', *mount, '--tmpfs', '/app/data/plugins:rw,size=65536,mode=0700', '-p', '127.0.0.1:18083:18083', args.docker_image], check=True, stdout=subprocess.DEVNULL)
    try:
        verify('missing')
        subprocess.run(['docker', 'cp', str(restored / 'full'), name + ':/app/full'], check=True)
        current = json.loads((restored / 'v2/descriptor.json').read_text())
        subprocess.run(['docker', 'exec', name, '/zcard', 'plugin', 'disable', '--conf', '/app/conf', '--id', 'member-purchase-gate', '--expected-generation', '8'], check=True)
        subprocess.run(['docker', 'exec', name, '/zcard', 'plugin', 'import', '--conf', '/app/conf', '--id', 'member-purchase-gate', '--action', 'upgrade', '--expected-generation', '9', '--digest', current['archiveSHA256'], '--descriptor', '/app/v2/descriptor.json', '--signature', '/app/v2/signature.ed25519', '--archive', '/app/v2/plugin.zplug', '--approve-scopes'], check=True)
        subprocess.run(['docker', 'exec', name, '/zcard', 'plugin', 'enable', '--conf', '/app/conf', '--id', 'member-purchase-gate', '--expected-generation', '10', '--digest', current['archiveSHA256'], '--approve-scopes'], check=True)
        large = json.loads((restored / 'full/descriptor.json').read_text())
        failed = subprocess.run(['docker', 'exec', name, '/zcard', 'plugin', 'import', '--conf', '/app/conf', '--id', 'member-purchase-gate', '--action', 'upgrade', '--expected-generation', '11', '--digest', large['archiveSHA256'], '--descriptor', '/app/full/descriptor.json', '--signature', '/app/full/signature.ed25519', '--archive', '/app/full/plugin.zplug', '--approve-scopes'], capture_output=True, text=True)
        assert failed.returncode != 0 and 'no space left' in (failed.stdout + failed.stderr), failed.stdout + failed.stderr
        verify('disk-full-old-runtime')
        print('PASS real ENOSPC staging preserves old runtime and rules', flush=True)
    finally:
        subprocess.run(['docker', 'rm', '-f', name], check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'volume', 'rm', volume], check=True, stdout=subprocess.DEVNULL)
    print('PASS no-Node container install / recreate / persisted rules', flush=True)
