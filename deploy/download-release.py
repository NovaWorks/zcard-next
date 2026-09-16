#!/usr/bin/env python3
"""Download a signed ZCard release; never execute an unverified binary."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

PUBLIC_KEY = "e7d28f99b52cda5e2596c4bea8b125c8c29cb4ca07af83aa5fe2a898c0e587cd"
RELEASE_BASE = "https://github.com/NovaWorks/zcard-next/releases/download"
LATEST_API = "https://api.github.com/repos/NovaWorks/zcard-next/releases/latest"


def fetch(url, dest, limit):
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "zcard-docker-installer"})
            with urllib.request.urlopen(request, timeout=60) as response, dest.open("wb") as output:
                size = 0
                while chunk := response.read(1024 * 1024):
                    size += len(chunk)
                    if size > limit:
                        raise ValueError("下载文件超过预期大小")
                    output.write(chunk)
            return
        except (urllib.error.URLError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def verify_manifest(raw, public_key, work):
    manifest = json.loads(raw)
    signature = base64.b64decode(manifest.get("signature", ""), validate=True)
    if len(signature) != 64:
        raise ValueError("发行清单签名无效")
    content = dict(manifest, signature="")
    canonical = json.dumps(content, ensure_ascii=False, separators=(",", ":"))
    for char in "<>&\u2028\u2029":
        canonical = canonical.replace(char, "\\u%04x" % ord(char))
    (work / "content").write_bytes(canonical.encode())
    (work / "signature").write_bytes(signature)
    (work / "public.der").write_bytes(bytes.fromhex("302a300506032b6570032100" + public_key))
    result = subprocess.run([
        "openssl", "pkeyutl", "-verify", "-pubin", "-inkey", str(work / "public.der"),
        "-keyform", "DER", "-rawin", "-in", str(work / "content"),
        "-sigfile", str(work / "signature"),
    ], capture_output=True)
    if result.returncode:
        raise ValueError("发行清单签名校验失败")
    return manifest


def download(version, arch, output, base_url=RELEASE_BASE):
    if arch not in ("amd64", "arm64"):
        raise ValueError("仅支持 linux/amd64 和 linux/arm64")
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".zcard-release-", dir=output.parent) as tmp:
        work = Path(tmp)
        if version in ("", "latest", "auto", "dev"):
            fetch(LATEST_API, work / "latest.json", 1024 * 1024)
            version = json.loads((work / "latest.json").read_bytes())["tag_name"]
        if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version):
            raise ValueError("版本须为 vX.Y.Z 或 latest")
        base = base_url.rstrip("/") + "/" + version
        fetch(base + "/update.json", work / "update.json", 8 * 1024 * 1024)
        manifest = verify_manifest((work / "update.json").read_bytes(), PUBLIC_KEY, work)
        if manifest.get("version") != version or manifest.get("channel") != "stable":
            raise ValueError("发行清单版本或通道不匹配")
        asset = "zcard-linux-" + arch
        entries = [entry for entry in manifest.get("files", []) if entry.get("name") == asset]
        if len(entries) != 1:
            raise ValueError("发行清单缺少或重复包含目标架构")
        entry = entries[0]
        size, digest = entry.get("size"), entry.get("sha256", "")
        if type(size) is not int or not 0 < size <= 512 * 1024 * 1024 or not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise ValueError("发行文件校验信息无效")
        fetch(base + "/SHA256SUMS", work / "SHA256SUMS", 64 * 1024)
        rows = [line.split() for line in (work / "SHA256SUMS").read_text().splitlines()]
        sums = [row[0] for row in rows if len(row) == 2 and row[1].lstrip("*") == asset]
        if sums != [digest]:
            raise ValueError("SHA256SUMS 与签名清单不一致")
        print(f"下载 {version} ({arch})", flush=True)
        binary = work / asset
        fetch(base + "/" + asset, binary, size)
        with binary.open("rb") as stream:
            actual = hashlib.file_digest(stream, "sha256").hexdigest()
        if binary.stat().st_size != size or actual != digest:
            raise ValueError("安装包大小或 SHA256 校验失败")
        binary.chmod(0o755)
        os.replace(binary, output)
        print(f"已验证 {version} ({arch}) 的 ED25519 签名、大小及 SHA256", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="latest")
    parser.add_argument("--arch", required=True)
    parser.add_argument("--output", default="/out/zcard")
    parser.add_argument("--base-url", default=RELEASE_BASE, help="发行文件镜像根地址，其下按 tag 分目录")
    args = parser.parse_args()
    try:
        download(args.version, args.arch, args.output, args.base_url)
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"下载发行包失败：{error}\n")
