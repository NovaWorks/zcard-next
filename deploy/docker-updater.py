#!/usr/bin/env python3
"""Local, authenticated update controller for the official single-instance Compose stack.

The web application never receives the Docker socket. Only trusted host deployment
files control Docker arguments; HTTP requests contain a version, not a command.
"""
import fcntl
import hmac
from http.server import BaseHTTPRequestHandler
import importlib.util
import json
import os
from pathlib import Path
import socketserver
import subprocess
import threading
import time
import urllib.request
import uuid

spec = importlib.util.spec_from_file_location("container_release", Path(__file__).with_name("container-release.py"))
metadata = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metadata)


class Controller:
    def __init__(self, state, deploy, project, helper_image):
        self.state = Path(state)
        self.deploy = Path(deploy)
        self.project = project
        self.helper_image = helper_image
        self.lock = threading.Lock()
        self.job = self.read("job.json", {"phase": "idle", "busy": False})

    def read(self, name, default=None):
        path = self.state / name
        return json.loads(path.read_text()) if path.exists() else default

    def save(self, **changes):
        self.job.update(changes)
        metadata.atomic_json(self.state / "job.json", self.job)
        print(json.dumps({k: self.job.get(k) for k in ("id", "phase", "error")}, ensure_ascii=False), flush=True)

    def compose(self, *args, **kwargs):
        return self.command([
            "docker", "compose", "--project-directory", str(self.deploy), "-p", self.project,
            "--env-file", str(self.deploy / ".env"),
            "--env-file", str(self.state / "installation.env"),
            "-f", str(self.deploy / "docker-compose.yml"),
            "-f", str(self.deploy / "docker-online.yml"), "-f", str(self.state / "image.json"),
            *args,
        ], **kwargs)

    def command(self, args, timeout=600, output=None):
        # Never return raw Docker stderr: it can contain deployment credentials.
        result = subprocess.run(args, stdout=output or subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, text=output is None)
        if result.returncode:
            raise RuntimeError("Docker 操作失败（" + " ".join(args[:2]) + "），请在服务器检查部署日志")
        return result.stdout.strip() if output is None else ""

    def container(self):
        ids = self.compose("ps", "-a", "-q", "zcard").splitlines()
        if len(ids) != 1:
            raise ValueError("仅支持官方 Compose 单实例部署")
        info = json.loads(self.command(["docker", "inspect", ids[0]]))[0]
        labels = info["Config"].get("Labels") or {}
        if labels.get("com.docker.compose.project") != self.project or labels.get("com.docker.compose.service") != "zcard":
            raise ValueError("目标容器与当前站点不匹配")
        if info["Config"].get("Cmd") != ["serve", "-conf", "/app/configs"]:
            raise ValueError("仅支持默认 all 模式与 /app/configs 配置")
        mounts = [v for v in info["Mounts"] if v["Destination"] == "/app" and v["Type"] == "volume"]
        if len(mounts) != 1:
            raise ValueError("应用数据必须使用官方 /app 命名卷")
        return info, mounts[0]["Name"]

    def health(self, version=None):
        try:
            with urllib.request.urlopen("http://zcard:8000/health", timeout=4) as response:
                data = json.load(response)
            if not data.get("status", {}).get("server") or not data["status"].get("database"):
                return None
            if version and data.get("version") != version:
                return None
            return data
        except (OSError, ValueError, KeyError):
            return None

    def wait_health(self, version, timeout=180):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            if self.health(version):
                return
            time.sleep(2)
        raise RuntimeError("新版应用版本或数据库健康检查超时")

    def status(self):
        current = self.read("current.json", {})
        with self.lock:
            result = dict(self.job)
        previous = self.read("previous.json", {})
        result.update(ready=True, protocol=metadata.PROTOCOL,
                      rollback_version=previous.get("version", ""),
                      rollback_ready=bool(previous and previous.get("schema") == current.get("schema")
                                          and not result.get("busy")))
        # The backend supplies its actual running version, not this cached value.
        return result

    def start(self, version, current, dialect, rollback=False):
        metadata.version_tuple(version)
        metadata.version_tuple(current)
        if dialect not in ("sqlite", "mysql"):
            raise ValueError("在线更新支持 SQLite 或官方 Compose 内置 MySQL；外部数据库请使用手动升级")
        with self.lock:
            if self.job.get("busy"):
                return dict(self.job)
            installed = self.read("current.json", {})
            if installed.get("version") != current:
                raise ValueError("运行版本与部署记录不一致，请修复部署后重试")
            if rollback:
                previous = self.read("previous.json", {})
                if previous.get("version") != version or previous.get("schema") != installed.get("schema"):
                    raise ValueError("上一版本数据库结构不兼容，不能直接回退镜像")
            elif metadata.version_tuple(version) <= metadata.version_tuple(current):
                raise ValueError("目标版本必须高于当前版本")
            self.job = {"id": uuid.uuid4().hex, "phase": "checking", "busy": True,
                        "target": version, "previous": current, "dialect": dialect,
                        "rollback": rollback, "error": "", "progress": 0, "stopped": False}
            self.save()
            threading.Thread(target=self.run, daemon=True).start()
            return dict(self.job)

    def pin(self, image):
        metadata.atomic_json(self.state / "image.json", {"services": {"zcard": {"image": image}}})

    def backup(self, volume):
        dest = self.state / "backups" / self.job["id"]
        dest.mkdir(parents=True, mode=0o700)
        # Resolve bind sources on the Docker host, never using the helper's /state path.
        host_state = self.deploy / ".docker-update"
        self.command(["docker", "run", "--rm", "--network", "none", "--entrypoint", "tar",
                      "--mount", "type=volume,src=" + volume + ",dst=/app,readonly",
                      "--mount", "type=bind,src=" + str(host_state) + ",dst=/state",
                      self.helper_image, "-czf", "/state/backups/" + self.job["id"] + "/app.tar.gz",
                      "-C", "/app", "."], timeout=1800)
        if self.job["dialect"] == "mysql":
            with (dest / "mysql.sql").open("wb") as output:
                self.compose("exec", "-T", "mysql", "sh", "-c",
                             'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --single-transaction --no-tablespaces --set-gtid-purged=OFF -u"$MYSQL_USER" "$MYSQL_DATABASE"',
                             output=output, timeout=1800)
            if not (dest / "mysql.sql").stat().st_size:
                raise RuntimeError("MySQL 备份为空")
        metadata.atomic_json(dest / "deployment.json", self.job["old"])
        self.save(backup_dir=str(host_state / "backups" / self.job["id"]))

    def finish(self):
        target, old = self.job["release"], self.job["old"]
        actual, _ = self.container()
        expected = json.loads(self.command(["docker", "image", "inspect", target["image"]]))[0]["Id"]
        if actual["Image"] != expected:
            raise ValueError("实际运行镜像与目标签名镜像不一致")
        metadata.atomic_json(self.state / "previous.json", old)
        metadata.atomic_json(self.state / "current.json", target)
        self.save(phase="rolled_back" if self.job.get("rollback") else "idle", busy=False, progress=100)

    def restore(self, reason):
        old = self.job.get("old")
        target = self.job.get("release")
        if not self.job.get("stopped"):
            self.save(phase="failed", busy=False, error=reason)
            return
        # Once the new process could have migrated the database, require exact schema equality.
        safe = not self.job.get("new_started") or (old and target and old["schema"] == target["schema"])
        self.compose("stop", "-t", "60", "zcard", timeout=90)
        if not safe:
            self.save(phase="failed", busy=False,
                      error=reason + "；数据库可能已迁移，已停止应用。请从备份核对恢复，禁止直接降级镜像。")
            return
        self.pin(old["image"])
        self.compose("up", "-d", "--no-build", "--no-deps", "zcard")
        self.wait_health(old["version"])
        metadata.atomic_json(self.state / "current.json", old)
        self.save(phase="rolled_back", busy=False, error=reason + "；已恢复更新前版本")

    def run(self):
        try:
            old = self.read("current.json")
            target = metadata.resolve(self.job["target"])
            if self.job.get("rollback") and old["schema"] != target["schema"]:
                raise ValueError("回退版本数据库结构不兼容")
            info, volume = self.container()
            # Detect manual image changes even when the embedded version was not changed.
            expected = json.loads(self.command(["docker", "image", "inspect", old["image"]]))[0]["Id"]
            if info["Image"] != expected:
                raise ValueError("运行镜像与签名部署记录不一致")
            health = self.health(old["version"])
            if not health or health["dialect"] != self.job["dialect"]:
                raise ValueError("旧应用健康或数据库类型不匹配")
            self.save(old=old, release=target, phase="downloading")
            self.command(["docker", "pull", target["image"]], timeout=1800)
            # Durable intent BEFORE stopping; recovery can safely restart the old application.
            self.save(phase="backing_up", stopped=True)
            self.compose("stop", "-t", "60", "zcard", timeout=90)
            stopped, _ = self.container()
            if stopped["State"]["Running"] or stopped["State"].get("ExitCode") == 137:
                raise RuntimeError("应用未正常退出，停止升级以保护在途任务")
            self.backup(volume)
            self.save(phase="applying")
            self.pin(target["image"])
            self.save(new_started=True, phase="restarting")
            self.compose("up", "-d", "--no-build", "--no-deps", "zcard")
            self.save(phase="verifying")
            self.wait_health(target["version"])
            self.finish()
        except Exception as error:
            try:
                self.restore(str(error))
            except Exception:
                self.save(phase="failed", busy=False, error="更新及自动恢复失败，请在服务器检查部署并保留备份")

    def recover(self):
        if not self.job.get("busy"):
            return
        try:
            if self.job.get("new_started"):
                self.wait_health(self.job["target"], timeout=60)
                self.finish()
            else:
                self.restore("升级助手重启，未完成的任务已中止")
        except Exception:
            try:
                self.restore("升级助手重启后未能确认新版本健康")
            except Exception:
                self.save(phase="failed", busy=False, error="恢复任务失败，请在服务器检查部署并保留备份")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, code, body):
        raw = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def authorized(self):
        return hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + self.server.token)

    def do_GET(self):
        if not self.authorized():
            return self.reply(403, {"error": "unauthorized"})
        if self.path != "/status":
            return self.reply(404, {"error": "not found"})
        self.reply(200, self.server.controller.status())

    def do_POST(self):
        if not self.authorized():
            return self.reply(403, {"error": "unauthorized"})
        if self.path not in ("/apply", "/rollback"):
            return self.reply(404, {"error": "not found"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 4096:
                raise ValueError("请求大小无效")
            self.connection.settimeout(10)
            body = json.loads(self.rfile.read(length))
            if set(body) != {"version", "current", "dialect"} or not all(isinstance(x, str) for x in body.values()):
                raise ValueError("请求参数无效")
            self.reply(200, self.server.controller.start(body["version"], body["current"], body["dialect"], self.path == "/rollback"))
        except (ValueError, KeyError, TypeError) as error:
            self.reply(400, {"error": str(error)})
        except Exception:
            self.reply(503, {"error": "升级助手不可用"})


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


def main():
    os.umask(0o077)
    state = Path("/state")
    state.mkdir(parents=True, exist_ok=True)
    # Hold a process-wide lock across recovery and the whole server lifetime.
    with (state / "controller.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        token = os.environ["ZCARD_UPDATER_TOKEN"]
        if len(token) < 32:
            raise ValueError("升级助手凭据无效")
        controller = Controller(state, os.environ["ZCARD_DEPLOY_DIR"], os.environ["ZCARD_COMPOSE_PROJECT"], os.environ["ZCARD_UPDATER_IMAGE"])
        sock = Path("/run/zcard-update/updater.sock")
        sock.parent.mkdir(parents=True, exist_ok=True)
        os.chmod(sock.parent, 0o755)
        sock.unlink(missing_ok=True)
        with Server(str(sock), Handler) as server:
            os.chmod(sock, 0o666)  # Per-installation bearer token authenticates the nonroot app.
            server.token, server.controller = token, controller
            threading.Thread(target=controller.recover, daemon=True).start()
            server.serve_forever()


if __name__ == "__main__":
    main()
