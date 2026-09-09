#!/usr/bin/env bash
# ZCard Linux 安装 / 管理。需 bash、curl、python3；生产推荐 systemd。
# sudo bash zcard-install.sh install [--bin ./zcard-linux-amd64] [--db sqlite|mysql|postgres]
# sudo bash zcard-install.sh update [self-update 参数，如 -source github]
# sudo bash zcard-install.sh status|logs|start|stop|restart|uninstall
# ZCARD_INSTALL_DIR=/opt/zcard，ZCARD_PORT=8000，ZCARD_VERSION=latest（仅首次下载）。
set -Eeuo pipefail
umask 077

readonly GH_REPO="${ZCARD_GH_REPO:-NovaWorks/zcard-next}"
readonly VERSION="${ZCARD_VERSION:-latest}"
readonly PORT="${ZCARD_PORT:-8000}"
readonly INSTALL_DIR="${ZCARD_INSTALL_DIR:-/opt/zcard}"
readonly CONF_DIR="${INSTALL_DIR}/configs"
readonly DATA_DIR="${INSTALL_DIR}/data"
readonly BIN="${INSTALL_DIR}/zcard"
readonly UNIT_FILE="${ZCARD_UNIT_FILE:-/etc/systemd/system/zcard.service}"
readonly SERVICE=zcard
log() { printf '[INFO] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }
have_systemd() { [ -d /run/systemd/system ] && command -v systemctl >/dev/null; }
need_root() {
  [ "$(uname -s)" = Linux ] || die '此脚本仅支持 Linux'
  if [ "$INSTALL_DIR" = /opt/zcard ] || have_systemd; then
    [ "$(id -u)" = 0 ] || die '请以 root / sudo 运行'
  fi
  # 配置路径同时用于 systemd unit 与 SQLite URI，限制为普通绝对路径。
  [[ "$INSTALL_DIR" =~ ^/[a-zA-Z0-9_./-]+$ ]] || die '安装目录必须为无空格的普通绝对路径'
}
require_command() { command -v "$1" >/dev/null || die "缺少 $1，请先安装"; }
svc() { systemctl "$@" "$SERVICE.service"; }

arch_name() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die '仅支持 amd64 / arm64' ;;
  esac
}

# 首装从同一个明确 tag 下载二进制和 SHA256SUMS，拒绝校验失败的文件。
download_bin() (
  local dest="$1" arch tmp tag asset base
  arch="$(arch_name)"; tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  tag="$VERSION"
  if [ "$tag" = latest ]; then
    tag="$(curl -fsSL --retry 3 "https://api.github.com/repos/${GH_REPO}/releases/latest" | python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"])')"
  fi
  [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die '版本须为 vX.Y.Z'
  asset="zcard-linux-${arch}"
  base="https://github.com/${GH_REPO}/releases/download/${tag}"
  log "下载 ${tag} (${arch})"
  curl -fSL --retry 3 --connect-timeout 15 -o "$tmp/$asset" "$base/$asset"
  curl -fsSL --retry 3 --connect-timeout 15 -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
  python3 - "$tmp" "$asset" <<'PYTHON'
import hashlib, pathlib, sys
root, name = pathlib.Path(sys.argv[1]), sys.argv[2]
rows = [line.split() for line in (root/'SHA256SUMS').read_text().splitlines()]
expected = [r[0] for r in rows if len(r) == 2 and r[1].lstrip('*') == name]
with (root/name).open('rb') as f:
    digest = hashlib.sha256()
    for block in iter(lambda: f.read(1024 * 1024), b''): digest.update(block)
    actual = digest.hexdigest()
if len(expected) != 1 or actual != expected[0]:
    sys.exit('SHA256 校验失败，中止安装')
PYTHON
  install -m 755 "$tmp/$asset" "$dest"
)

ask() {
  local ans=""
  if [ -t 0 ]; then read -r -p "$1 [$2]: " ans; fi
  printf '%s\n' "${ans:-$2}"
}
ask_secret() {
  local ans=""
  if [ -t 0 ]; then
    read -r -s -p "$1: " ans
    printf '\n' >&2
  fi
  printf '%s\n' "$ans"
}
resolve_db() {
  DB_DIALECT="${DB_DIALECT:-}"
  if [ -z "$DB_DIALECT" ]; then
    DB_DIALECT="$(ask '数据库 postgres / mysql / sqlite' sqlite)"
  fi
  case "$DB_DIALECT" in sqlite|mysql|postgres) ;; *) die '数据库须为 sqlite / mysql / postgres';; esac
  DB_HOST="${DB_HOST:-}"; DB_NAME="${DB_NAME:-}"
  DB_USER="${DB_USER:-}"; DB_PASS="${DB_PASS:-}"; DB_PORT="${DB_PORT:-}"
  REDIS_ADDR="${REDIS_ADDR:-}"; REDIS_PASS="${REDIS_PASS:-}"
  if [ "$DB_DIALECT" = sqlite ]; then return; fi
  local def_port=5432 def_user=postgres
  if [ "$DB_DIALECT" = mysql ]; then def_port=3306; def_user=root; fi
  DB_HOST="${DB_HOST:-$(ask '数据库主机' '127.0.0.1')}"
  DB_PORT="${DB_PORT:-$(ask '数据库端口' "$def_port")}"
  DB_USER="${DB_USER:-$(ask '数据库用户' "$def_user")}"
  DB_PASS="${DB_PASS:-$(ask_secret '数据库密码')}"
  DB_NAME="${DB_NAME:-$(ask '数据库名（不存在将创建）' 'zcard')}"
  REDIS_ADDR="${REDIS_ADDR:-$(ask 'Redis 地址' '127.0.0.1:6379')}"
  REDIS_PASS="${REDIS_PASS:-$(ask_secret 'Redis 密码（可留空）')}"
  [ -n "$DB_PASS" ] || die '数据库密码不能为空'
}

write_config() {
  mkdir -p "$CONF_DIR" "$DATA_DIR"
  # JSON 是 YAML 的子集；标准库负责转义密码，PG 用户信息使用 URL 编码。
  export DB_DIALECT DB_HOST DB_PORT DB_USER DB_PASS DB_NAME REDIS_ADDR REDIS_PASS
  python3 - "$CONF_DIR/config.yaml" "$DATA_DIR" "$PORT" <<'PYTHON'
import json, os, secrets, sys
from urllib.parse import quote
path, data, port = sys.argv[1:]
e = os.environ
driver = e['DB_DIALECT']
source = 'file:' + data + '/zcard.db'
if driver == 'mysql':
    source = '{}:{}@tcp({}:{})/{}?parseTime=True&loc=UTC&charset=utf8mb4'.format(e['DB_USER'], e['DB_PASS'], e['DB_HOST'], e['DB_PORT'], e['DB_NAME'])
elif driver == 'postgres':
    host = e['DB_HOST']
    if ':' in host and not host.startswith('['): host = '[' + host + ']'
    source = 'postgres://{}:{}@{}:{}/{}?sslmode=disable'.format(quote(e['DB_USER'], safe=''), quote(e['DB_PASS'], safe=''), host, e['DB_PORT'], quote(e['DB_NAME'], safe=''))
security = {}
for key in ('jwt_admin_key', 'jwt_user_key', 'card_key', 'data_key'):
    value = e.get('ZCARD_' + key.upper()) or secrets.token_hex(32)
    if key in ('card_key', 'data_key'):
        try: valid = len(value) == 64 and len(bytes.fromhex(value)) == 32
        except ValueError: valid = False
    else: valid = len(value.encode()) >= 32
    if not valid: sys.exit('密钥格式错误: ' + key)
    security[key] = value
cfg = {'server': {'http': {'addr': '0.0.0.0:' + port, 'timeout': '30s'}, 'grpc': {'addr': ''}, 'migrate_on_start': True},
       'data': {'database': {'driver': driver, 'source': source, 'max_open_conns': 20, 'max_idle_conns': 5},
                'redis': {'addr': e['REDIS_ADDR'], 'password': e['REDIS_PASS'], 'read_timeout': '0.2s', 'write_timeout': '0.2s'}},
       'security': security, 'tenancy': {'mode': 'row'}, 'log': {'level': 'info', 'format': 'text'}}
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f: json.dump(cfg, f, ensure_ascii=False, indent=2); f.write('\n')
PYTHON
  unset DB_PASS REDIS_PASS
  log '已生成配置和四把持久密钥（0600）；请备份 configs 与 data'
}

write_unit() {
  cat > "$UNIT_FILE" <<EOF
[Unit]
Description=ZCard
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BIN} serve -conf ${CONF_DIR}
Environment=ZCARD_SUPERVISOR=systemd
EnvironmentFile=-${INSTALL_DIR}/zcard.env
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$UNIT_FILE"
  systemctl daemon-reload
}
wait_healthy() {
  local attempt response
  for ((attempt=0; attempt<60; attempt++)); do
    if svc is-active --quiet && response="$(curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/health")" &&
      python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(not (d.get("status",{}).get("database") and d["status"].get("server")))' <<< "$response"; then
      return 0
    fi
    sleep 1
  done
  journalctl -u "$SERVICE" -n 30 --no-pager >&2 || true
  return 1
}
ensure_backup_tools() {
  local tool package
  case "$DB_DIALECT" in
    postgres) tool=pg_dump; package=postgresql-client ;;
    mysql) tool=mysqldump; package=default-mysql-client ;;
    *) return 0 ;;
  esac
  if ! command -v "$tool" >/dev/null; then
    if command -v apt-get >/dev/null && [ "$(id -u)" = 0 ]; then
      if ! apt-get update -qq || ! apt-get install -y "$package"; then warn "请自行安装 ${tool}"; fi
    else warn "请自行安装 ${tool}"; fi
  fi
  if [ "$DB_DIALECT" = postgres ]; then warn '请核对 pg_dump 大版本不低于 PostgreSQL 服务器版本'; fi
}

do_install() {
  need_root; require_command curl; require_command python3
  if ! [[ "$PORT" =~ ^[1-9][0-9]{0,4}$ ]] || ((PORT > 65535)); then die 'ZCARD_PORT 必须为 1~65535'; fi
  [ ! -e "$BIN" ] && [ ! -e "$CONF_DIR/config.yaml" ] || die '已有部署：安装不会覆盖二进制或密钥；升级请用 update，修复旧部署请参阅部署指南'
  if have_systemd && [ -e "$UNIT_FILE" ]; then die 'zcard 服务已存在，请先核对旧部署；不会覆盖服务单元'; fi
  local bin_src=""
  while [ "$#" -gt 0 ]; do
    [ "$#" -ge 2 ] || die "缺少参数值: $1"
    case "$1" in
      --bin) bin_src="$2" ;; --db) DB_DIALECT="$2" ;;
      --db-host) DB_HOST="$2" ;; --db-port) DB_PORT="$2" ;;
      --db-user) DB_USER="$2" ;; --db-pass) DB_PASS="$2" ;; --db-name) DB_NAME="$2" ;;
      --redis) REDIS_ADDR="$2" ;; --redis-pass) REDIS_PASS="$2" ;;
      *) die "未知参数: $1" ;;
    esac
    shift 2
  done
  resolve_db
  mkdir -p "$INSTALL_DIR"
  if [ -n "$bin_src" ]; then install -m 755 "$bin_src" "$BIN.new"; else download_bin "$BIN.new"; fi
  "$BIN.new" version >/dev/null || { rm -f "$BIN.new"; die '二进制不可执行'; }
  if [ "$DB_DIALECT" != sqlite ]; then
    if ! "$BIN.new" dbtest -dialect "$DB_DIALECT" -host "$DB_HOST" -port "$DB_PORT" \
      -user "$DB_USER" -password "$DB_PASS" -name "$DB_NAME" -redis "$REDIS_ADDR" -redis-password "$REDIS_PASS"; then
      rm -f "$BIN.new"; die '数据库或 Redis 校验失败'
    fi
  fi
  write_config
  mv "$BIN.new" "$BIN"
  ensure_backup_tools
  if have_systemd; then
    write_unit
    svc enable
    svc restart
    wait_healthy || die '服务未通过健康检查，安装未完成'
    log '安装完成：服务健康，已启用开机自启'
  else warn "未检测到 systemd；请运行: cd ${INSTALL_DIR} && ./zcard serve -conf configs"; fi
  log "浏览器访问 http://服务器IP:${PORT}/install 设置管理员；Nginx / HTTPS 按部署指南配置"
}

do_update() {
  need_root; require_command curl; require_command python3
  [ -x "$BIN" ] || die '尚未安装'
  # 不接受裸二进制覆盖：统一经过内置的数据库备份、签名校验和更新状态机。
  local arg
  for arg in "$@"; do
    case "$arg" in --bin|--bin=*|-conf|--conf|-conf=*|--conf=*|--rollback|-rollback|--rollback=*|-rollback=*) die '此参数请使用部署指南中的独立运维流程';; esac
  done
  cd "$INSTALL_DIR"
  local state_before=""
  if [ -f update.state ]; then state_before="$(cat update.state)"; fi
  "$BIN" self-update -y -conf "$CONF_DIR" "$@"
  if have_systemd; then
    wait_healthy || die '更新后服务不健康，请检查日志与更新状态；未报告更新成功'
    if [ -f update.state ] && [ "$(cat update.state)" != "$state_before" ]; then
      python3 - <<'PYTHON'
import json, time, sys
for _ in range(60):
    with open('update.state') as f: state = json.load(f)
    if state.get('rolled_back'): sys.exit('更新失败，程序已回滚；请检查日志')
    if state.get('status') == 'ok': break
    time.sleep(1)
else: sys.exit('更新尚未通过启动确认，请检查 update.state 与服务日志')
PYTHON
    fi
  else warn '文件已处理；请通过原进程管理器重启并检查 /health'; fi
}

do_uninstall() {
  need_root
  if have_systemd; then svc stop; svc disable; fi
  rm -f "$UNIT_FILE"
  if have_systemd; then systemctl daemon-reload; fi
  log "已移除服务，配置、二进制与数据保留在 ${INSTALL_DIR}"
}
main() {
  case "${1:-help}" in
    install) shift; do_install "$@" ;;
    update) shift; do_update "$@" ;;
    uninstall) do_uninstall ;;
    status) have_systemd || die '无 systemd'; svc status --no-pager ;;
    logs) have_systemd || die '无 systemd'; journalctl -u "$SERVICE" -n 100 --no-pager -f ;;
    start|stop|restart) need_root; have_systemd || die '无 systemd'; svc "$1" ;;
    -h|--help|help) sed -n '2,6p' "$0" ;;
    *) die "未知命令: $1" ;;
  esac
}
if [[ "${BASH_SOURCE[0]}" = "$0" ]]; then main "$@"; fi
