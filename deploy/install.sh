#!/usr/bin/env bash
# 旧入口兼容层：统一使用 scripts/zcard-install.sh，默认执行 install。
set -Eeuo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$script_dir/../scripts/zcard-install.sh" ]; then
  exec bash "$script_dir/../scripts/zcard-install.sh" "${@:-install}"
fi
# 单独下载此文件时，先保存主脚本，再执行，交互输入不会被管道占用。
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsSL --retry 3 "https://raw.githubusercontent.com/${ZCARD_GH_REPO:-NovaWorks/zcard-next}/${ZCARD_INSTALL_REF:-main}/scripts/zcard-install.sh" -o "$tmp"
bash "$tmp" "${@:-install}"
