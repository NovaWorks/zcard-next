"""Docker installation/persistence smoke test (isolated containers and volume).

ZCARD_TEST_IMAGE=zcard:test python3 scripts/test_docker.py
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def main():
    image = os.environ.get("ZCARD_TEST_IMAGE", "zcard:test")
    name = "zcard-smoke-" + uuid.uuid4().hex[:12]
    volume = name + "-data"
    docker("volume", "create", volume)
    with tempfile.TemporaryDirectory(prefix="zcard-docker-smoke-") as tmp:
        root = Path(tmp)
        try:
            def start():
                docker("run", "-d", "--name", name, "-p", "127.0.0.1::8000",
                       "-v", volume + ":/app", image)
                port = docker("port", name, "8000").splitlines()[0]
                return "http://" + port

            def request(path, payload=None):
                req = urllib.request.Request(base + path,
                    data=json.dumps(payload).encode() if payload is not None else None,
                    headers={"Content-Type": "application/json"})
                with urllib.request.urlopen(req, timeout=3) as r:
                    return r.read(), r.headers.get("Content-Type", "")

            def ready():
                for _ in range(120):
                    try:
                        body, _ = request("/health")
                        health = json.loads(body)
                        if health["status"]["server"] and health["status"]["database"]:
                            return health
                    except (OSError, ValueError, KeyError):
                        pass
                    time.sleep(0.5)
                raise AssertionError("Application never became healthy")

            def config_hash(filename):
                docker("cp", name + ":/app/configs/config.yaml", str(root / filename))
                return hashlib.sha256((root / filename).read_bytes()).hexdigest()

            base = start()
            assert ready()["dialect"] == "sqlite"
            result, _ = request("/api/v1/admin/install", {
                "admin_username": "deployment-smoke", "admin_password": secrets.token_urlsafe(24),
                "site_name": "Deployment smoke", "site_url": base, "dialect": "sqlite"})
            assert json.loads(result)["installed"] is True
            for path in ("/", "/admin/", "/install"):
                html, mime = request(path)
                assert "text/html" in mime, (path, mime)
                if path == "/admin/":
                    assets = re.findall(rb'(?:src|href)="(/admin/[^" ]+\.(?:js|css))"', html)
                    assert assets, "Admin bundle must use /admin/ base"
                    for asset in assets:
                        _, asset_mime = request(asset.decode())
                        assert "text/html" not in asset_mime, (asset, asset_mime)
            before = config_hash("before.yaml")
            # A readable upload fixture exercises the actual persisted media path.
            upload = root / "uploads"
            upload.mkdir(mode=0o755)
            png = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/ncAAAAASUVORK5CYII=")
            (upload / "persistence.png").write_bytes(png)
            (upload / "persistence.png").chmod(0o644)
            docker("cp", str(upload), name + ":/app/data/uploads")
            assert request("/uploads/persistence.png")[0] == png
            docker("rm", "-f", name)
            base = start()
            assert ready()["dialect"] == "sqlite"
            state, _ = request("/api/v1/admin/install/status")
            assert json.loads(state)["installed"] is True, "Database lost on container recreation"
            assert config_hash("after.yaml") == before, "Persistent keys/config changed"
            assert request("/uploads/persistence.png")[0] == png, "Upload lost on recreation"
            print("PASS: image startup, SQLite web install, frontend assets, container recreation, configuration/keys and uploaded files")
        finally:
            subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            docker("volume", "rm", volume)


if __name__ == "__main__":
    main()
