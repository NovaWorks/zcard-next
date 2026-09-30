"""Real backend API -> helper -> registry -> container recreation acceptance.

Requires ZCARD_ONLINE_TEST_OLD / NEW binaries compiled as v1.2.95 / v1.2.96,
and zcard-updater:dev. Test-only registry/manifest overrides stay in temp files.
"""
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def run(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError("command failed: " + " ".join(args[:3]) + "\n" + result.stderr[-3000:])
    return result.stdout.strip()


class Quiet(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def main():
    prefix = "zcard-online-" + uuid.uuid4().hex[:8]
    registry = prefix + "-registry"
    projects = []
    with tempfile.TemporaryDirectory(prefix=prefix + "-", dir="/tmp") as tmp:
        root = Path(tmp).resolve()
        server = ThreadingHTTPServer(("0.0.0.0", 0), partial(Quiet, directory=str(root / "releases")))
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            run("docker", "run", "-d", "--name", registry, "-p", "0.0.0.0:" + os.environ.get("ZCARD_TEST_REGISTRY_PORT", "15095") + ":5000", "registry:2.8.3")
            port = run("docker", "port", registry, "5000").split(":")[-1]
            repo = "localhost:" + port
            for _ in range(30):
                try:
                    urllib.request.urlopen("http://" + repo + "/v2/", timeout=2).close()
                    break
                except OSError:
                    time.sleep(1)
            apprepo, helperrepo = repo + "/zcard-next", repo + "/zcard-updater"
            run("docker", "tag", "zcard-updater:dev", helperrepo + ":test")
            run("docker", "push", helperrepo + ":test")
            helper = json.loads(run("docker", "image", "inspect", helperrepo + ":test"))[0]["RepoDigests"]
            helper = next(x for x in helper if x.startswith(helperrepo + "@"))
            images = {}
            for version, binary in (("v1.2.95", os.environ["ZCARD_ONLINE_TEST_OLD"]), ("v1.2.96", os.environ["ZCARD_ONLINE_TEST_NEW"]), ("v1.2.97", None)):
                build = root / ("build-" + version)
                build.mkdir()
                if binary:
                    shutil.copy2(binary, build / "zcard-linux-arm64")
                    for folder in ("configs", "data", "backups"):
                        (build / "runtime" / folder).mkdir(parents=True)
                    shutil.copy2(ROOT / "deploy/Dockerfile.packaged", build / "Dockerfile")
                else:
                    (build / "Dockerfile").write_text('FROM alpine:3.22\nENTRYPOINT ["/does-not-exist"]\n')
                tag = apprepo + ":" + version
                run("docker", "build", "--build-arg", "TARGETARCH=arm64", "-t", tag, str(build))
                run("docker", "push", tag)
                images[version] = next(x for x in json.loads(run("docker", "image", "inspect", tag))[0]["RepoDigests"] if x.startswith(apprepo + "@"))
                release = root / "releases" / version
                release.mkdir(parents=True)
                descriptor = {"version": version, "protocol": 1, "image": images[version], "updater_image": helper,
                              "schema": {d: "a" * 64 for d in ("sqlite", "mysql", "postgres")}}
                (release / "zcard-container-release.json").write_text(json.dumps(descriptor))
                run("go", "-C", str(ROOT / "server"), "run", "./cmd/zcard", "self-update", "sign", "--key", str(ROOT / "server/.zcard-update-key"), "--dir", str(release), "--version", version)
            for dialect in os.environ.get("ZCARD_TEST_DIALECTS", "sqlite,mysql").split(","):
                project = prefix + "-" + dialect
                deploy = root / project
                deploy.mkdir()
                for name in ("docker-compose.yml", "docker-online.yml", "download-release.py", "container-release.py", "docker-online-install.py"):
                    shutil.copy2(ROOT / "deploy" / name, deploy / name)
                secret = secrets.token_hex(32)
                env = {"COMPOSE_PROJECT_NAME": project, "ZCARD_PORT": "0", "ZCARD_BIND": "127.0.0.1", "ZCARD_VERSION": "v1.2.95"}
                for key in ("ZCARD_JWT_ADMIN_KEY", "ZCARD_JWT_USER_KEY", "ZCARD_CARD_KEY", "ZCARD_DATA_KEY", "MYSQL_PASSWORD", "MYSQL_ROOT_PASSWORD"):
                    env[key] = secret
                (deploy / ".env").write_text("".join(f"{k}={v}\n" for k, v in env.items()))
                wrapper = deploy / "fixture-controller.py"
                wrapper.write_text(f'''import importlib.util
s=importlib.util.spec_from_file_location("controller","/opt/zcard-updater/docker-updater.py")
m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
m.metadata.APP_REPO={apprepo!r};m.metadata.UPDATER_REPO={helperrepo!r}
m.metadata.release.RELEASE_BASE="http://host.docker.internal:{server.server_port}"
original=m.Controller.wait_health
m.Controller.wait_health=lambda self, version, timeout=180: original(self,version,min(timeout,20))
backup=m.Controller.backup
def pause_backup(self, volume):
 if (self.state/"pause-backup").exists():
  (self.state/"backup-paused").touch()
  m.time.sleep(60)
 return backup(self,volume)
m.Controller.backup=pause_backup
m.main()
''')
                online = (deploy / "docker-online.yml").read_text().replace("    restart: unless-stopped", '    entrypoint: ["python3", "' + str(wrapper) + '"]\n    restart: unless-stopped')
                (deploy / "docker-online.yml").write_text(online)
                spec = importlib.util.spec_from_file_location("installer", deploy / "docker-online-install.py")
                installer = importlib.util.module_from_spec(spec);spec.loader.exec_module(installer)
                installer.metadata.APP_REPO, installer.metadata.UPDATER_REPO = apprepo, helperrepo
                installer.metadata.release.RELEASE_BASE = "http://127.0.0.1:" + str(server.server_port)
                # Record cleanup before any installer side effect.
                compose = ["docker", "compose", "--project-directory", str(deploy), "--env-file", str(deploy / ".env"), "-p", project]
                projects.append(compose)
                # Start a real v1.2.94 site, then exercise the one-time migration.
                original_compose = (deploy / "docker-compose.yml").read_text()
                (deploy / "docker-compose.yml").write_text(original_compose.replace('zcard-local:${ZCARD_VERSION:-latest}', 'nealbaker/zcard-next:v1.2.94'))
                run(*compose, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180")
                base = "http://" + run(*compose, "port", "zcard", "8000").splitlines()[0]
                def request(path, payload=None, token=None):
                    headers = {"Content-Type": "application/json"}
                    if token: headers["Authorization"] = "Bearer " + token
                    req = urllib.request.Request(base + path, data=json.dumps(payload).encode() if payload is not None else None, headers=headers)
                    with urllib.request.urlopen(req, timeout=10) as response:
                        return json.load(response)
                password = secrets.token_urlsafe(24)
                for _ in range(40):
                    try:
                        if request("/health").get("status", {}).get("database"): break
                    except Exception: pass
                    time.sleep(1)
                install = {"admin_username": "online-test", "admin_password": password, "site_name": "Online test", "site_url": base, "dialect": dialect}
                if dialect == "mysql":
                    install.update(db_host="mysql", db_port=3306, db_user="zcard", db_password=secret, db_name="zcard", redis_addr="redis:6379")
                installed = request("/api/v1/admin/install", install)
                assert installed.get("installed") or installed.get("restart_required"), installed
                for _ in range(90):
                    try:
                        if not request("/api/v1/admin/install/status").get("installed"):
                            time.sleep(1)
                            continue
                        token = request("/api/v1/admin/auth/login", {"username": "online-test", "password": password})["access_token"]
                        break
                    except Exception: time.sleep(1)
                else:
                    raise AssertionError("Installation did not complete")
                assert request("/health")["dialect"] == dialect
                installer.install("v1.2.95")
                base = "http://" + run(*compose, "port", "zcard", "8000").splitlines()[0]
                assert request("/api/v1/admin/install/status")["installed"]
                assert list((deploy / ".docker-update/backups").glob("bootstrap-*/app.tar.gz"))
                def status(): return request("/api/v1/admin/update/status", token=token)
                assert status()["docker_update_ready"], status()
                before = run(*compose, "ps", "-q", "zcard")
                def update(version, expected, phase, rollback=False, restart_during_backup=False):
                    if restart_during_backup:
                        (deploy / ".docker-update/pause-backup").touch()
                    request("/api/v1/admin/update/" + ("rollback" if rollback else "apply"), {"version": version}, token)
                    if restart_during_backup:
                        for _ in range(40):
                            if (deploy / ".docker-update/backup-paused").exists(): break
                            time.sleep(1)
                        assert (deploy / ".docker-update/backup-paused").exists()
                        run(*compose, "restart", "updater")
                    deadline = time.monotonic() + 100
                    last = None
                    while time.monotonic() < deadline:
                        try:
                            # Port 0 maps to a new port after container recreation.
                            nonlocal base
                            base = "http://" + run(*compose, "port", "zcard", "8000").splitlines()[0]
                            last = status()
                            if not last.get("busy", False) and last["phase"] == phase and last["current_version"] == expected:
                                return last
                        except Exception: pass
                        time.sleep(1)
                    raise AssertionError(last or run(*compose, "logs", "--tail", "15", "updater"))
                result = update("v1.2.96", "v1.2.96", "idle")
                assert result["backup_dir"]
                assert run(*compose, "ps", "-q", "zcard") != before
                assert request("/health")["dialect"] == dialect
                backup = Path(result["backup_dir"])
                assert (backup / "app.tar.gz").stat().st_size > 0
                if dialect == "mysql": assert (backup / "mysql.sql").stat().st_size > 0
                # Default Compose invocation must retain the pinned upgraded image.
                config = json.loads(run(*compose, "config", "--format", "json"))
                assert config["services"]["zcard"]["image"] == images["v1.2.96"]
                run(*compose, "restart", "updater")
                time.sleep(2)
                assert status()["target_version"] == "v1.2.96"
                result = update("v1.2.97", "v1.2.96", "rolled_back")
                assert result["error_message"]
                result = update("v1.2.95", "v1.2.95", "rolled_back", rollback=True)
                assert not result.get("error_message")
                result = update("v1.2.96", "v1.2.95", "rolled_back", restart_during_backup=True)
                assert "重启" in result["error_message"]
                assert request("/api/v1/admin/install/status")["installed"]
                print("PASS:", dialect, "API update, signed metadata, backup, recreation, persistent version, helper restart and failed-start recovery", flush=True)
        finally:
            for compose in projects:
                subprocess.run([*compose, "down", "-v"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(["docker", "rm", "-f", registry], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            server.shutdown();server.server_close()


if __name__ == "__main__":
    main()
