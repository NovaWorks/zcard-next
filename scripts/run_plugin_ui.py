"""Run plugin UI acceptance using a fresh temporary SQLite database and signing key.
Build first: make -C server web-dist build-fullstack VERSION=v1.2.89 SKIP_WEB_DIST=1
Requires local Playwright/Chrome; overrides PLAYWRIGHT_MODULE and CHROME_PATH are supported.
Never connects to production. Keeps fixture and screenshots for diagnosis; always stops its server.
"""
import datetime
import hashlib
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import time
import urllib.request

REPO = Path(__file__).resolve().parents[1]
for port in (18083, 19093):
    with socket.socket() as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        probe.bind(("127.0.0.1", port))
root = Path(subprocess.check_output(["go", "run", "scripts/fixtures/plugin-ui.go"], cwd=REPO, text=True).strip())
print("Isolated fixture:", root, flush=True)
for version in (1, 2):
    subprocess.run(["go", "run", "./tools/plugin-pack", "--manifest", str(root / f"manifest{version}.json"),
                    "--script", str(REPO / "examples/plugins/member-purchase-gate/main.js"),
                    "--key", str(root / "key.pem"), "--key-id", "ui-test", "--out", str(root / f"v{version}")],
                   cwd=REPO / "server", check=True, stdout=subprocess.DEVNULL)
with (root / "server.log").open("w") as log:
    process = subprocess.Popen([str(REPO / "server/bin/zcard"), "serve", "-conf", str(root / "conf")], cwd=root, stdout=log, stderr=log)
    try:
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError(f"Fixture failed; see {root / 'server.log'}")
            try:
                with urllib.request.urlopen("http://127.0.0.1:18083/health", timeout=1) as response:
                    if response.status == 200:
                        break
            except OSError:
                time.sleep(.1)
        else:
            raise RuntimeError("Fixture health timeout")
        login = json.loads((root / "login.json").read_text())
        subprocess.run([str(REPO / "server/bin/zcard"), "admin", "create", "--conf", str(root / "conf"),
                        "--username", login["username"], "--password", login["password"]], cwd=root, check=True, stdout=subprocess.DEVNULL)
        for role in ("operator", "support"):
            subprocess.run([str(REPO / "server/bin/zcard"), "admin", "create", "--conf", str(root / "conf"),
                            "--username", "p3-" + role, "--password", login["password"], "--role", role], cwd=root, check=True, stdout=subprocess.DEVNULL)
        # The test bypasses only the installer wizard in its own fresh database.
        # All tested plugin/configuration/order mutations use the actual authenticated HTTP API.
        with sqlite3.connect(root / "data/zcard.db") as db:
            now = datetime.datetime.now(datetime.timezone.utc).isoformat()
            db.execute('INSERT INTO settings (created_at,updated_at,"group","key",value) VALUES (?,?,?,?,?)',
                       (now, now, "ops", "installed_at", json.dumps(now)))
        env = dict(os.environ, ZCARD_TEST_BINARY=str(REPO / "server/bin/zcard"), ZCARD_TEST_BASE_URL="http://127.0.0.1:18083", ZCARD_TEST_FIXTURE=str(root))
        (root / "source.json").write_text(json.dumps({"pid": process.pid, "head": subprocess.check_output(["git", "rev-parse", "HEAD"],cwd=REPO,text=True).strip(), "binary_sha256": hashlib.sha256((REPO / "server/bin/zcard").read_bytes()).hexdigest(), "process_started": subprocess.check_output(["ps", "-p", str(process.pid), "-o", "lstart="], text=True).strip(), "binary_mtime": (REPO / "server/bin/zcard").stat().st_mtime}))
        subprocess.run(["node", os.environ.get("ZCARD_PLUGIN_TEST_SCRIPT", "scripts/test_plugin_ui.cjs")], cwd=REPO, env=env, check=True)
        assert process.poll() is None, "server exited during hot lifecycle test"
        before = json.loads((root / "source.json").read_text())
        assert before["process_started"] == subprocess.check_output(["ps", "-p", str(process.pid), "-o", "lstart="], text=True).strip()
        print("PASS same serving process and start time:", process.pid, before["process_started"], flush=True)
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
