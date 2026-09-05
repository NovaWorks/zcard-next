#!/usr/bin/env bash
# ZCard 一键安装 / 管理脚本（Linux 服务器；比传统发卡系统更简——单二进制 + 内嵌 SQLite）。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/NovaWorks/zcard-next/main/scripts/zcard-install.sh -o /tmp/zcard-install.sh
#   sudo bash /tmp/zcard-install.sh install                    # 交互式（选数据库：PostgreSQL 推荐/MySQL/SQLite）
#   sudo bash /tmp/zcard-install.sh install --db postgres --db-host 127.0.0.1 --db-port 5432 \
#        --db-user postgres --db-pass xxx --db-name zcard --redis 127.0.0.1:6379   # 免交互直装 PG
#   sudo bash /tmp/zcard-install.sh install --bin ./bin/zcard  # 本地二进制安装（无 Releases 时）
#   sudo bash /tmp/zcard-install.sh update / status / start / stop / restart / uninstall / logs
#
# 数据库规则（与 Web 安装向导一致）：PostgreSQL（推荐·生产首选）/ MySQL 需配 Redis，
# 库不存在自动创建（安装前用 zcard dbtest 真实校验）；SQLite 免一切依赖（本地测试模式）。
#
# 安装内容：/opt/zcard/{zcard,configs/config.yaml,data/} + systemd 服务（自动重启/开机自启）。
# 安装后浏览器打开 http://服务器IP:8000 → 在线安装向导（选 PostgreSQL 推荐 / SQLite 本地测试）。
#
# 环境变量：ZCARD_VERSION（默认 latest）｜ZCARD_PORT（默认 8000）｜ZCARD_GH_REPO（默认 NovaWorks/zcard-next）

set -Eeuo pipefail

readonly GH_REPO="${ZCARD_GH_REPO:-NovaWorks/zcard-next}"
readonly VERSION="${ZCARD_VERSION:-latest}"
readonly PORT="${ZCARD_PORT:-8000}"
# 目录可覆盖（默认 /opt/zcard）——测试/定制部署用：ZCARD_INSTALL_DIR=/srv/zcard
readonly INSTALL_DIR="${ZCARD_INSTALL_DIR:-/opt/zcard}"
readonly CONF_DIR="${INSTALL_DIR}/configs"
readonly DATA_DIR="${INSTALL_DIR}/data"
readonly BIN="${INSTALL_DIR}/zcard"
readonly UNIT_FILE="${ZCARD_UNIT_FILE:-/etc/systemd/system/zcard.service}"
readonly SERVICE="zcard"

log()  { printf '\033[36m[INFO]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[WARN]\033[0m %s\n' "$*"; }
die()  { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

have_systemd() { [ "$(ps -p 1 -o comm= 2>/dev/null | tr -d '[:space:]')" = "systemd" ]; }

need_root() {
  # 显式覆盖安装目录（测试/定制）时放宽 root 要求；默认 /opt 与 systemd 仍需 root
  if [ "${ZCARD_INSTALL_DIR:-}" != "" ] || [ "${ZCARD_UNIT_FILE:-}" != "" ]; then return 0; fi
  [ "$(id -u)" = 0 ] || die "请用 root 运行（sudo bash $0 ...）"
}

arch_name() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "不支持的架构 $(uname -m)（支持 amd64/arm64）" ;;
  esac
}

# download_bin <目标路径>：GitHub Releases 下载（zcard-linux-<arch>.tar.gz 或裸二进制）
download_bin() {
  local dest="$1" arch tmp url
  arch="$(arch_name)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  local vtag="$VERSION"
  if [ "$vtag" = "latest" ]; then
    vtag="$(curl -fsSL "https://api.github.com/repos/${GH_REPO}/releases/latest" | grep -m1 '"tag_name"' | cut -d'"' -f4)"
    [ -n "$vtag" ] || die "无法获取最新版本（检查网络/GitHub Releases）"
  fi
  log "下载 ZCard ${vtag} (linux/${arch}) ..."
  for url in \
    "https://github.com/${GH_REPO}/releases/download/${vtag}/zcard-linux-${arch}.tar.gz" \
    "https://github.com/${GH_REPO}/releases/download/${vtag}/zcard-linux-${arch}"; do
    if curl -fSL --retry 3 -o "${tmp}/dl" "$url" 2>/dev/null; then
      if tar -tzf "${tmp}/dl" >/dev/null 2>&1; then
        tar -xzf "${tmp}/dl" -C "$tmp"
        local inner="$(find "$tmp" -type f -name zcard | head -1)"
        [ -n "$inner" ] || die "压缩包内未找到 zcard 二进制"
        mv "$inner" "$dest"
      else
        mv "${tmp}/dl" "$dest"
      fi
      chmod +x "$dest"
      return 0
    fi
  done
  die "下载失败：Releases 未发布 ${vtag} 资产？可用 --bin ./path/to/zcard 本地安装"
}

write_config() {
  mkdir -p "$CONF_DIR" "$DATA_DIR"
  if [ -f "${CONF_DIR}/config.yaml" ]; then
    warn "配置已存在，保留不动（${CONF_DIR}/config.yaml）——如需更换数据库请删除后重装"
    return
  fi
  local db_driver=sqlite db_source="file:${DATA_DIR}/zcard.db"
  local redis_addr="127.0.0.1:6379" redis_pass=""
  if [ "${DB_DIALECT:-sqlite}" = "mysql" ]; then
    db_driver=mysql
    db_source="${DB_USER}:${DB_PASS}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?parseTime=True&loc=UTC&charset=utf8mb4"
    redis_addr="$REDIS_ADDR"; redis_pass="$REDIS_PASS"
  elif [ "${DB_DIALECT:-sqlite}" = "postgres" ]; then
    db_driver=postgres
    db_source="postgres://${DB_USER}:${DB_PASS}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"
    redis_addr="$REDIS_ADDR"; redis_pass="$REDIS_PASS"
  fi
  cat > "${CONF_DIR}/config.yaml" <<EOF
# ZCard 一键安装生成（数据库：${DB_DIALECT:-sqlite}）
# 结构对齐 config.example.yaml：http/grpc 须嵌套在 server 段下（漏包裹=端口配置失效）
server:
  http:
    addr: 0.0.0.0:${PORT}
    timeout: 30s
  grpc:
    addr: 0.0.0.0:$((PORT + 1000))
    timeout: 30s
  migrate_on_start: true
  admin_base_path: ""
data:
  database:
    driver: ${db_driver}
    source: "${db_source}"
    max_open_conns: 20
    max_idle_conns: 5
  redis:
    addr: ${redis_addr}
    password: "${redis_pass}"
    read_timeout: 0.2s
EOF
  log "已生成配置 ${CONF_DIR}/config.yaml（端口 ${PORT}；数据库 ${DB_DIALECT:-sqlite}$([ "${DB_DIALECT:-sqlite}" != sqlite ] && echo " @ ${DB_HOST}:${DB_PORT}/${DB_NAME}")）"
}

write_unit() {
  cat > "$UNIT_FILE" <<EOF
[Unit]
Description=ZCard 商城系统（单二进制 + 内嵌 SQLite）
After=network.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BIN} serve -conf ${CONF_DIR}
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

svc() { systemctl "$1" "${SERVICE}" 2>/dev/null || true; }

# ask <提示> <默认值>：TTY 交互读入（无 TTY 返回默认）
ask() {
  local def="$2" ans=""
  if [ -t 0 ]; then
    printf '\033[36m[?]\033[0m %s [%s]: ' "$1" "$def" >&2
    read -r ans || true
  fi
  echo "${ans:-$def}"
}

ask_secret() {
  local def="$2" ans=""
  if [ -t 0 ]; then
    printf '\033[36m[?]\033[0m %s%s: ' "$1" "$([ -n "$def" ] && echo " [${def}]" || echo '（无则留空）')" >&2
    read -r ans || true
  fi
  echo "${ans:-$def}"
}

# resolve_db：决定 DB 选择（参数优先 → 交互菜单 → sqlite 默认）
resolve_db() {
  DB_DIALECT="${DB_ARGS_DIALECT:-}"
  if [ -z "$DB_DIALECT" ] && [ -t 0 ]; then
    echo ""
    echo "  ┌─────────────── 选择数据库 ───────────────┐"
    echo "  │ 1) PostgreSQL   推荐 · 生产首选          │"
    echo "  │ 2) MySQL        自托管标准形态           │"
    echo "  │ 3) SQLite       本地测试（免配置免Redis）│"
    echo "  └──────────────────────────────────────────┘"
    DB_DIALECT="$(ask '请选择数据库（1/2/3）' '1')"
    case "$DB_DIALECT" in
      1|postgres|postgresql|pg) DB_DIALECT=postgres ;;
      2|mysql) DB_DIALECT=mysql ;;
      3|sqlite) DB_DIALECT=sqlite ;;
      *) die "无效选择：$DB_DIALECT" ;;
    esac
  fi
  [ -z "$DB_DIALECT" ] && DB_DIALECT=sqlite
  case "$DB_DIALECT" in postgres|mysql|sqlite) ;; *) die "无效数据库类型：$DB_DIALECT（postgres|mysql|sqlite）";; esac

  if [ "$DB_DIALECT" = "sqlite" ]; then
    if [ -t 0 ]; then
      echo ""
      warn "SQLite 为本地测试模式：不支持分站多租户等高级功能，生产环境建议 PostgreSQL。"
    fi
    return
  fi

  local def_host="127.0.0.1" def_port=5432 def_user="postgres"
  [ "$DB_DIALECT" = "mysql" ] && { def_port=3306; def_user="root"; }
  DB_HOST="${DB_ARGS_HOST:-$(ask '数据库主机' "$def_host")}"
  DB_PORT="${DB_ARGS_PORT:-$(ask '数据库端口' "$def_port")}"
  DB_USER="${DB_ARGS_USER:-$(ask '数据库用户' "$def_user")}"
  DB_PASS="${DB_ARGS_PASS:-$(ask_secret '数据库密码' '')}"
  DB_NAME="${DB_ARGS_NAME:-$(ask '数据库名（不存在自动创建）' 'zcard')}"
  REDIS_ADDR="${REDIS_ARGS_ADDR:-$(ask 'Redis 地址（必配）' '127.0.0.1:6379')}"
  REDIS_PASS="${REDIS_ARGS_PASS:-$(ask_secret 'Redis 密码' '')}"
  [ -n "$DB_PASS" ] || die "数据库密码不能为空（${DB_DIALECT} 模式）"
}

# db_validate：zcard dbtest 真实校验（连接/权限/自动建库/Redis ping）
db_validate() {
  log "校验 ${DB_DIALECT} 与 Redis 连接（库不存在将自动创建）..."
  if ! "${BIN}" dbtest -dialect "$DB_DIALECT" -host "$DB_HOST" -port "$DB_PORT" \
      -user "$DB_USER" -password "$DB_PASS" -name "$DB_NAME" \
      -redis "$REDIS_ADDR" -redis-password "$REDIS_PASS" 2>&1; then
    die "数据库/Redis 校验失败（检查地址、账号密码与权限）"
  fi
}


# ensure_backup_tools 预装数据库备份工具（在线更新前强制 DB 备份依赖 pg_dump/mysqldump；
# 缺失则更新会被 fail-closed 中止——安装时装好比事后踩坑好）。按预置方言精准装，
# 未指定（交互向导后选）则两个都尽力；非 Debian 系降级为提示。
ensure_backup_tools() {
  local want_pg=0 want_my=0
  case "$DB_ARGS_DIALECT" in
    postgres) want_pg=1 ;;
    mysql)    want_my=1 ;;
    *)        want_pg=1; want_my=1 ;;
  esac
  if [ "$want_pg" = 1 ] && ! command -v pg_dump >/dev/null; then
    apt-get install -y postgresql-client >/dev/null 2>&1 \
      && c_green "已预装 postgresql-client（更新前备份依赖）" \
      || c_yellow "pg_dump 未就绪：选 PostgreSQL 时在线更新前需自行安装 postgresql-client"
  fi
  if [ "$want_my" = 1 ] && ! command -v mysqldump >/dev/null; then
    apt-get install -y default-mysql-client >/dev/null 2>&1 || true
  fi
}

do_install() {
  need_root
  local bin_src=""
  local args=("$@")
  local i=0
  # 解析预置参数（--db*/--redis* 免交互；--bin 本地二进制）
  while [ $((i + 1)) -le ${#args[@]} ]; do
    case "${args[$i]}" in
      --db)      DB_ARGS_DIALECT="${args[$((i + 1))]}"; i=$((i + 2)) ;;
      --db-host) DB_ARGS_HOST="${args[$((i + 1))]}";    i=$((i + 2)) ;;
      --db-port) DB_ARGS_PORT="${args[$((i + 1))]}";    i=$((i + 2)) ;;
      --db-user) DB_ARGS_USER="${args[$((i + 1))]}";    i=$((i + 2)) ;;
      --db-pass) DB_ARGS_PASS="${args[$((i + 1))]}";    i=$((i + 2)) ;;
      --db-name) DB_ARGS_NAME="${args[$((i + 1))]}";    i=$((i + 2)) ;;
      --redis)   REDIS_ARGS_ADDR="${args[$((i + 1))]}"; i=$((i + 2)) ;;
      --redis-pass) REDIS_ARGS_PASS="${args[$((i + 1))]}"; i=$((i + 2)) ;;
      --bin)     bin_src="${args[$((i + 1))]}";         i=$((i + 2)) ;;
      *)         i=$((i + 1)) ;;
    esac
  done
  ensure_backup_tools

  resolve_db
  mkdir -p "$INSTALL_DIR"
  # 二进制：本地 or Releases（先落临时位，校验可执行后启用——失败不影响在跑服务）
  if [ -n "$bin_src" ]; then
    [ -f "$bin_src" ] || die "本地二进制不存在：$bin_src"
    install -m 755 "$bin_src" "${BIN}.new"
  else
    download_bin "${BIN}.new"
  fi
  "${BIN}.new" -h >/dev/null 2>&1 || { rm -f "${BIN}.new"; die "二进制不可执行（架构不匹配？）"; }
  mv "${BIN}.new" "$BIN"

  [ "$DB_DIALECT" != "sqlite" ] && db_validate
  write_config
  if have_systemd; then
    write_unit
    if svc is-active >/dev/null 2>&1; then svc restart; else svc enable --now; fi
    sleep 2
    svc is-active >/dev/null 2>&1 || { journalctl -u "$SERVICE" -n 20 --no-pager; die "服务启动失败（上方为日志）"; }
    log "安装完成并已启动（开机自启）"
  else
    warn "未检测到 systemd——文件已就绪，手动启动：cd ${INSTALL_DIR} && ${BIN} serve -conf ${CONF_DIR}"
  fi
  echo ""
  echo "  ➜ 数据库: $([ "$DB_DIALECT" = "sqlite" ] && echo "SQLite（本地测试模式）" || echo "${DB_DIALECT} @ ${DB_HOST}:${DB_PORT}/${DB_NAME}")"
  echo "  ➜ 浏览器打开: http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo 服务器IP):${PORT}"
  echo "  ➜ 在线安装向导自动进入（设置管理员即完成）"
  echo "  ➜ 管理命令: bash $0 {status|logs|restart|update|uninstall}"
}

do_update() {
  need_root
  [ -f "$BIN" ] || die "未安装（先 install）"
  cp "$BIN" "${BIN}.bak"
  if [ "${1:-}" = "--bin" ] && [ -n "${2:-}" ]; then
    [ -f "$2" ] || die "本地二进制不存在：$2"
    install -m 755 "$2" "${BIN}.new"
  else
    download_bin "${BIN}.new"
  fi
  mv "${BIN}.new" "$BIN"
  have_systemd && svc restart
  sleep 3
  if have_systemd && ! svc is-active >/dev/null 2>&1; then
    warn "新版本启动失败——回滚上一版本"
    mv "${BIN}.bak" "$BIN"; svc restart
    die "更新失败已回滚（journalctl -u ${SERVICE} -n 30 查原因）"
  fi
  rm -f "${BIN}.bak"
  log "更新完成（启动迁移已自动应用）"
}

do_uninstall() {
  need_root
  svc stop; svc disable 2>/dev/null || true
  rm -f "$UNIT_FILE"; systemctl daemon-reload 2>/dev/null || true
  warn "已停止并移除服务。数据保留在 ${DATA_DIR}（确认放弃再手动删除：rm -rf ${INSTALL_DIR}）"
}

main() {
  case "${1:-}" in
    install)   shift; do_install "$@" ;;
    update)    shift; do_update "$@" ;;
    uninstall) do_uninstall ;;
    status)    have_systemd && systemctl status "$SERVICE" --no-pager || die "无 systemd" ;;
    logs)      have_systemd && journalctl -u "$SERVICE" -n 100 --no-pager -f || die "无 systemd" ;;
    start|stop|restart) need_root; svc "${1}" ;;
    -h|--help|help|'')
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//' ;;
    *) die "未知命令 $1（install|update|uninstall|status|logs|start|stop|restart）" ;;
  esac
}

main "$@"
