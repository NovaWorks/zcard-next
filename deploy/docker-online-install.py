#!/usr/bin/env python3
"""One-time opt-in to managed Docker updates; run from docker-install.sh."""
import argparse
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time
import urllib.request

spec = importlib.util.spec_from_file_location("container_release", Path(__file__).with_name("container-release.py"))
metadata = importlib.util.module_from_spec(spec)
spec.loader.exec_module(metadata)


def run(args):
    return subprocess.check_output(args, text=True).strip()


def install(version):
    state = Path(__file__).resolve().parent / ".docker-update"
    state.mkdir(mode=0o700, exist_ok=True)
    with (state / "installer.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return install_locked(version)


def install_locked(version):
    os.umask(0o077)
    deploy = Path(__file__).resolve().parent
    state = deploy / ".docker-update"
    state.mkdir(mode=0o700, exist_ok=True)
    if (state / "installation.env").exists():
        raise ValueError("已启用后台在线更新，请在后台执行版本升级；恢复操作参见部署指南")
    info = metadata.resolve(version)
    base = ["docker", "compose", "--env-file", str(deploy / ".env"), "-f", str(deploy / "docker-compose.yml")]
    config = json.loads(run([*base, "config", "--format", "json"]))
    project = config["name"]
    existing = run([*base, "ps", "-a", "-q", "zcard"]).splitlines()
    if len(existing) > 1:
        raise ValueError("在线更新仅支持单实例")
    if existing:
        old = json.loads(run(["docker", "inspect", existing[0]]))[0]
        labels = old["Config"].get("Labels", {})
        if labels.get("com.docker.compose.project.working_dir") != str(deploy):
            raise ValueError("发现同名 Compose 项目，请在原部署目录运行，不能创建新数据卷替代原站点")
    # Pull and verify metadata before touching a running application.
    for image in (info["image"], info["updater_image"]):
        subprocess.run(["docker", "pull", image], check=True)
    settings = {
        "ZCARD_DEPLOY_DIR": str(deploy), "ZCARD_COMPOSE_PROJECT": project,
        "ZCARD_UPDATER_TOKEN": secrets.token_hex(32), "ZCARD_UPDATER_IMAGE": info["updater_image"],
    }
    for value in settings.values():
        if any(c in value for c in "\n\r'$ "):
            raise ValueError("部署目录不能包含空格、引号或环境变量字符")
    env = deploy / ".env"
    original = env.read_text()
    if any(line.startswith(k + "=") for line in original.splitlines() for k in (*settings, "COMPOSE_FILE")):
        raise ValueError(".env 已含在线更新配置，请先检查原部署")
    if existing:
        mounts = [m for m in old["Mounts"] if m["Type"] == "volume" and m["Destination"] == "/app"]
        if len(mounts) != 1 or old["Config"].get("Cmd") != ["serve", "-conf", "/app/configs"]:
            raise ValueError("只支持官方单实例部署与 /app 数据卷")
        volume = mounts[0]["Name"]
        dialect = run(["docker", "run", "--rm", "--network", "none", "--mount",
                       "type=volume,src=" + volume + ",dst=/app,readonly", info["image"], "docker-backup-check"])
        if dialect not in ("sqlite", "mysql"):
            raise ValueError("数据库不在自动备份支持范围内")
        backup = state / "backups" / ("bootstrap-" + time.strftime("%Y%m%d%H%M%S"))
        backup.mkdir(parents=True, mode=0o700)
        shutil.copy2(env, backup / "deployment.env")
        metadata.atomic_json(backup / "previous-image.json", {"image": old["Image"], "project": project})
        try:
            run(["docker", "stop", "-t", "60", existing[0]])
            stopped = json.loads(run(["docker", "inspect", existing[0]]))[0]
            if stopped["State"].get("ExitCode") == 137:
                raise RuntimeError("旧应用未正常退出，停止升级")
            run(["docker", "run", "--rm", "--network", "none", "--entrypoint", "tar",
                 "--mount", "type=volume,src=" + volume + ",dst=/app,readonly",
                 "--mount", "type=bind,src=" + str(backup) + ",dst=/backup",
                 info["updater_image"], "-czf", "/backup/app.tar.gz", "-C", "/app", "."])
            if dialect == "mysql":
                with (backup / "mysql.sql").open("wb") as output:
                    subprocess.run([*base, "exec", "-T", "mysql", "sh", "-c",
                                    'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --single-transaction --no-tablespaces --set-gtid-purged=OFF -u"$MYSQL_USER" "$MYSQL_DATABASE"'],
                                   stdout=output, check=True)
                if not (backup / "mysql.sql").stat().st_size:
                    raise RuntimeError("MySQL 备份为空")
            print("旧站点备份完成：" + str(backup), flush=True)
        except Exception:
            run(["docker", "start", existing[0]])
            raise
    (state / "installation.env").write_text("".join(f"{k}={v}\n" for k, v in settings.items()))
    metadata.atomic_json(state / "current.json", info)
    metadata.atomic_json(state / "image.json", {"services": {"zcard": {"image": info["image"]}}})
    # Standard `cd deploy && docker compose up -d` must read the same pinned image.
    metadata.atomic_json(state / "bootstrap.json", {"previous_container": existing[0] if existing else "", "version": version})
    with env.open("a") as stream:
        stream.write("\n# Managed Docker online updates\n")
        stream.write((state / "installation.env").read_text())
        stream.write("COMPOSE_FILE=" + ":".join(str(deploy / name) for name in
                     ("docker-compose.yml", "docker-online.yml", ".docker-update/image.json")) + "\n")
        stream.flush()
        os.fsync(stream.fileno())
    compose = ["docker", "compose", "--project-directory", str(deploy), "-p", project,
               "--env-file", str(env), "-f", str(deploy / "docker-compose.yml"),
               "-f", str(deploy / "docker-online.yml"), "-f", str(state / "image.json")]
    subprocess.run([*compose, "config", "--quiet"], check=True)
    subprocess.run([*compose, "up", "-d", "--no-build", "--wait", "--wait-timeout", "180"], check=True)
    address = run([*compose, "port", "zcard", "8000"]).splitlines()[0].replace("0.0.0.0", "127.0.0.1")
    for _ in range(90):
        try:
            with urllib.request.urlopen("http://" + address + "/health", timeout=3) as response:
                health = json.load(response)
            if health.get("version") == version and all(health.get("status", {}).get(k) for k in ("server", "database")):
                print("Docker 在线更新已启用：" + version + "。后续请在后台 系统设置 → 在线更新 操作。")
                return
        except (OSError, ValueError):
            pass
        time.sleep(2)
    raise RuntimeError("新应用健康检查失败，请在 deploy 目录查看 docker compose logs；保留 .docker-update 和原数据卷")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    install(args.version)
