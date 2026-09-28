"""Run P1-P4 against fresh local MySQL/PostgreSQL containers; always remove them.
No external database is accepted. Output defaults to a private temporary folder.
Usage: python3 scripts/run_plugin_databases.py [--output /tmp/my-acceptance]
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path)
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
out = args.output or Path(tempfile.mkdtemp(prefix='zcard-plugin-databases-'))
out.mkdir(parents=True, exist_ok=True)
prefix = 'zcard-p4-' + uuid.uuid4().hex[:10]
password = uuid.uuid4().hex
names = []
env = dict(os.environ)
def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)
try:
    for driver, image, port, setting in [('mysql', 'mysql:8', 3306, 'MYSQL_ROOT_PASSWORD'), ('postgres', 'postgres:16', 5432, 'POSTGRES_PASSWORD')]:
        name = prefix + '-' + driver
        run(['docker', 'run', '-d', '--name', name, '-e', setting + '=' + password, '-p', f'127.0.0.1::{port}', image], stdout=subprocess.DEVNULL)
        names.append(name)
        hostport = subprocess.check_output(['docker', 'port', name, str(port)], text=True).strip().split(':')[-1]
        ready = ['mysql', '-h127.0.0.1', '-uroot', '-p' + password, '-e', 'SELECT 1'] if driver == 'mysql' else ['pg_isready', '-U', 'postgres']
        for _ in range(120):
            if subprocess.run(['docker', 'exec', name, *ready], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError(driver + ' readiness timeout')
        def dsn(database):
            if driver == 'mysql':
                return f'root:{password}@tcp(127.0.0.1:{hostport})/{database}?parseTime=True&loc=UTC&multiStatements=true'
            return f'postgres://postgres:{password}@127.0.0.1:{hostport}/{database}?sslmode=disable'
        for suite in ['P1', 'P1_UPGRADE', 'P2', 'P2_SUPPLY']:
            database = 'p4_' + suite.lower()
            if driver == 'mysql':
                run(['docker', 'exec', '-e', 'MYSQL_PWD=' + password, name, 'mysql', '-uroot', '-e', 'CREATE DATABASE ' + database])
            else:
                run(['docker', 'exec', name, 'createdb', '-U', 'postgres', database])
            env[f'ZCARD_{suite}_{driver.upper()}_DSN'] = dsn(database)
        env['ZCARD_TEST_MYSQL_DSN' if driver == 'mysql' else 'ZCARD_TEST_PG_DSN'] = dsn('mysql' if driver == 'mysql' else 'postgres')
    for name, cmd in [('dialects', ['go', 'test', '-count=1', '-v', './internal/mods/plugin', './internal/mods/order', './internal/mods/supplier', './migrations']), ('integration', ['make', 'test-integration'])]:
        with (out / (name + '.log')).open('w') as log:
            run(cmd, cwd=root / 'server', env=env, stdout=log, stderr=subprocess.STDOUT)
        print('PASS', name, flush=True)
    (out / 'result.json').write_text(json.dumps({'mysql': '8', 'postgres': '16', 'redis': False, 'passed': True}))
finally:
    for name in names:
        subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, check=True)
print('Evidence:', out)
