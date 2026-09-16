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
from urllib.parse import urljoin, urlsplit


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def main():
    image = os.environ.get("ZCARD_TEST_IMAGE", "zcard:test")
    expected_version = os.environ.get("ZCARD_TEST_VERSION") or json.loads((Path(__file__).resolve().parents[1] / "server/CHANGELOG.json").read_text())[0]["version"]
    assert docker("run", "--rm", image, "version").split()[1] == expected_version
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
            health = ready()
            assert health["dialect"] == "sqlite"
            assert health["version"] == expected_version
            password = secrets.token_urlsafe(24)
            result, _ = request("/api/v1/admin/install", {
                "admin_username": "deployment-smoke", "admin_password": password,
                "site_name": "Deployment smoke", "site_url": base, "dialect": "sqlite"})
            assert json.loads(result)["installed"] is True
            for path in ("/", "/admin/", "/install"):
                html, mime = request(path)
                assert "text/html" in mime, (path, mime)
                if path == "/admin/":
                    assets = re.findall(rb'(?:src|href)="([^" ]+\.(?:js|css))"', html)
                    assert assets, "Admin bundle must include scripts/styles"
                    for asset in assets:
                        # Production embeds use relative URLs so custom admin paths work.
                        asset_path = urlsplit(urljoin(base + "/admin/", asset.decode())).path
                        assert asset_path.startswith("/admin/"), asset_path
                        _, asset_mime = request(asset_path)
                        assert "text/html" not in asset_mime, (asset, asset_mime)
            login, _ = request("/api/v1/admin/auth/login", {"username": "deployment-smoke", "password": password})
            token = json.loads(login)["access_token"]
            def update_request(path, mutate=False):
                return urllib.request.urlopen(urllib.request.Request(base + "/api/v1/admin/update/" + path,
                    data=b"{}" if mutate else None,
                    headers={"Content-Type": "application/json", "Authorization": "Bearer " + token}), timeout=5)
            with update_request("status") as response:
                status = json.load(response)
                assert status["supervisor_kind"] == "docker", status
                assert status["current_version"] == expected_version, status
            for action in ("apply", "rollback"):
                try:
                    update_request(action, True)
                    raise AssertionError("Container mutation was allowed: " + action)
                except urllib.error.HTTPError as error:
                    assert error.code == 403, error.code
                    assert json.load(error)["reason"] == "update.CONTAINER"
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
            health = ready()
            assert health["dialect"] == "sqlite"
            assert health["version"] == expected_version
            state, _ = request("/api/v1/admin/install/status")
            assert json.loads(state)["installed"] is True, "Database lost on container recreation"
            assert config_hash("after.yaml") == before, "Persistent keys/config changed"
            assert request("/uploads/persistence.png")[0] == png, "Upload lost on recreation"
            print("PASS: image version, container update guards, image startup, SQLite web install, frontend assets, container recreation, configuration/keys and uploaded files")
        finally:
            subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            docker("volume", "rm", volume)


if __name__ == "__main__":
    main()
