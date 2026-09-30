#!/usr/bin/env python3
"""Signed container metadata, shared by the installer and the local updater."""
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import tempfile

spec = importlib.util.spec_from_file_location("release_download", Path(__file__).with_name("download-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

APP_REPO = "docker.io/nealbaker/zcard-next"
UPDATER_REPO = "docker.io/nealbaker/zcard-updater"
ASSET = "zcard-container-release.json"
PROTOCOL = 1


def version_tuple(version):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("版本须为 vX.Y.Z")
    return tuple(map(int, version[1:].split(".")))


def validate(info, version):
    if info.get("version") != version or info.get("protocol") != PROTOCOL:
        raise ValueError("镜像版本或升级协议不兼容，请更新部署工具")
    for field, repo in (("image", APP_REPO), ("updater_image", UPDATER_REPO)):
        if not re.fullmatch(re.escape(repo) + r"@sha256:[a-f0-9]{64}", info.get(field, "")):
            raise ValueError("镜像必须来自官方仓库并固定 digest")
    if set(info.get("schema", {})) != {"sqlite", "mysql", "postgres"} or any(
        not re.fullmatch(r"[a-f0-9]{64}", value) for value in info["schema"].values()
    ):
        raise ValueError("缺少数据库迁移指纹")
    return info


def resolve(version):
    version_tuple(version)
    with tempfile.TemporaryDirectory(prefix="zcard-container-release-") as tmp:
        work = Path(tmp)
        base = release.RELEASE_BASE + "/" + version
        release.fetch(base + "/update.json", work / "update.json", 8 * 1024 * 1024)
        manifest = release.verify_manifest((work / "update.json").read_bytes(), release.PUBLIC_KEY, work)
        if manifest.get("version") != version or manifest.get("channel") != "stable":
            raise ValueError("签名清单版本或通道不匹配")
        entries = [f for f in manifest["files"] if f["name"] == ASSET]
        if len(entries) != 1 or type(entries[0]["size"]) is not int or not 0 < entries[0]["size"] <= 65536:
            raise ValueError("此版本尚未发布 Docker 在线更新镜像")
        entry = entries[0]
        release.fetch(base + "/" + ASSET, work / ASSET, entry["size"])
        raw = (work / ASSET).read_bytes()
        if len(raw) != entry["size"] or hashlib.sha256(raw).hexdigest() != entry["sha256"]:
            raise ValueError("镜像清单校验失败")
        return validate(json.loads(raw), version)


def atomic_json(path, value):
    import os
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as f:
            json.dump(value, f, ensure_ascii=False, indent=2)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(name):
            os.unlink(name)
