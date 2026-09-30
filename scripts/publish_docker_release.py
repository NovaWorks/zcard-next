#!/usr/bin/env python3
"""Publish multi-platform images and create metadata to include in update.json.

Run after building/testing the release binaries, BEFORE signing the final
manifest and publishing the GitHub Release. Does not move latest automatically.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--dir", type=Path, required=True)
    parser.add_argument("--builder")
    args = parser.parse_args()
    if json.loads((ROOT / "server/CHANGELOG.json").read_text())[0]["version"] != args.version:
        parser.error("版本必须匹配当前源码 CHANGELOG")
    dest = args.dir.resolve()
    for arch in ("amd64", "arm64"):
        if not (dest / ("zcard-linux-" + arch)).is_file():
            parser.error("缺少双架构发行包")
    for folder in ("configs", "data", "backups"):
        (dest / "runtime" / folder).mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="zcard-image-metadata-") as tmp:
        refs = {}
        for name, dockerfile, context in (("zcard-next", "Dockerfile.packaged", dest), ("zcard-updater", "Dockerfile.updater", ROOT)):
            output = Path(tmp) / (name + ".json")
            command = ["docker", "buildx", "build"]
            if args.builder: command.extend(["--builder", args.builder])
            command.extend(["--platform", "linux/amd64,linux/arm64", "--provenance=false", "-f", str(ROOT / "deploy" / dockerfile),
                            "-t", "nealbaker/" + name + ":" + args.version, "--metadata-file", str(output), "--push", str(context)])
            subprocess.run(command, check=True)
            refs[name] = "docker.io/nealbaker/" + name + "@" + json.loads(output.read_text())["containerimage.digest"]
    schema = {}
    for dialect in ("sqlite", "mysql", "postgres"):
        digest = hashlib.sha256()
        for path in sorted((ROOT / "server/migrations" / dialect).glob("*.sql")):
            digest.update(path.name.encode() + b"\0" + path.read_bytes() + b"\0")
        schema[dialect] = digest.hexdigest()
    descriptor = {"version": args.version, "protocol": 1, "image": refs["zcard-next"], "updater_image": refs["zcard-updater"], "schema": schema}
    (dest / "zcard-container-release.json").write_text(json.dumps(descriptor, indent=2) + "\n")
    print("镜像已发布。请重新签名 update.json（包含 zcard-container-release.json），校验后发布 Release。")


if __name__ == "__main__":
    main()
