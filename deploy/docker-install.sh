#!/usr/bin/env bash
# 在源码检出目录执行：bash deploy/docker-install.sh
# 自动生成一次性配置，构建镜像并启动 MySQL + Redis + ZCard。
set -Eeuo pipefail
umask 077
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
command -v docker >/dev/null || { echo '请先安装 Docker Engine 和 Compose v2' >&2; exit 1; }
for tool in curl python3; do
  command -v "$tool" >/dev/null || { echo "请先安装 $tool" >&2; exit 1; }
done
docker compose version >/dev/null
docker info >/dev/null
if [ ! -e "$script_dir/.env" ]; then
  command -v openssl >/dev/null || { echo '请先安装 openssl' >&2; exit 1; }
  # 原子、不可覆盖发布，失败不留下半份密钥；并发首装最多一个成功。
  tmp="$(mktemp "$script_dir/.env.tmp.XXXXXX")"
  trap 'rm -f "$tmp"' EXIT
  {
    printf 'ZCARD_VERSION=%s\nZCARD_BIND=%s\nZCARD_PORT=%s\n' "${ZCARD_VERSION:-dev}" "${ZCARD_BIND:-0.0.0.0}" "${ZCARD_PORT:-8000}"
    for key in ZCARD_JWT_ADMIN_KEY ZCARD_JWT_USER_KEY ZCARD_CARD_KEY ZCARD_DATA_KEY MYSQL_ROOT_PASSWORD MYSQL_PASSWORD; do
      secret_value="$(openssl rand -hex 32)"
      printf '%s=%s\n' "$key" "$secret_value"
    done
  } > "$tmp"
  chmod 600 "$tmp"
  ln "$tmp" "$script_dir/.env"
  rm -f "$tmp"
fi
compose=(docker compose --env-file "$script_dir/.env" -f "$script_dir/docker-compose.yml")
"${compose[@]}" config --quiet
"${compose[@]}" up -d --build --wait --wait-timeout 180
address="$("${compose[@]}" port zcard 8000)"
address="${address/0.0.0.0/127.0.0.1}"
ready=0
for ((attempt=0; attempt<60; attempt++)); do
  if response="$(curl -fsS --max-time 2 "http://${address}/health")" &&
    python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(not (d.get("status",{}).get("database") and d["status"].get("server")))' <<< "$response"; then
    ready=1; break
  fi
  sleep 1
done
[ "$ready" = 1 ] || { echo '应用健康检查失败，请查看 docker compose logs zcard' >&2; exit 1; }
printf '%s\n' '容器及应用健康检查通过。请打开配置端口的 /install 完成向导。' \
  'MySQL: 主机 mysql，端口 3306，用户/库名 zcard；密码为 deploy/.env 中 MYSQL_PASSWORD。' \
  'Redis: redis:6379，密码留空。请备份 deploy/.env 和 app-data / mysql-data / redis-data 卷。' \
  '安装前 /health 显示 sqlite（引导库），选择 MySQL 并完成向导后应为 mysql。'
