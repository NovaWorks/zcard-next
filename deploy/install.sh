#!/usr/bin/env bash
# ZCard 2.0 一键安装 + 菜单式管理（， 「一键脚本」）
#
# 安装： curl -fsSL <repo>/deploy/install.sh | bash （或下载后 sudo ./install.sh）
# 管理： zcard-manager （菜单：状态/日志/重启/更新/域名/改密/卸载）
#
# 布局（ ，migrate-from-v1 复用同一路径约定）：
# /opt/zcard/zcard 单二进制（self-update rename 舞步同目录）
# /opt/zcard/configs/ config.yaml（SQLite 起步，零依赖）
# /opt/zcard/data/zcard.db SQLite（WAL）
# /opt/zcard/uploads/ 媒体库
# systemd：zcard.service（Restart=always）+ zcard-rollback.service（OnFailure 自动回滚位）
# Nginx：80/443 反代 127.0.0.1:8000（SPA 保留路径透传）；certbot --nginx 签证书。

set -euo pipefail

INSTALL_DIR="/opt/zcard"
SERVICE_USER="zcard"
UNIT_NAME="zcard"
HTTP_PORT=8000
UPDATE_API="https://api.github.com/repos/NovaWorks/zcard-next"
DOWNLOAD_BASE="https://github.com/NovaWorks/zcard-next/releases/latest/download"

c_green() { printf '\033[32m%s\033[0m\n' "$*"; }
c_red()   { printf '\033[31m%s\033[0m\n' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m\n' "$*"; }
die() { c_red "错误: $*" >&2; exit 1; }

require_root() {
  [ "$(id -u)" -eq 0 ] || die "请以 root 运行（sudo $0）"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "不支持的架构: $(uname -m)（支持 linux/amd64、linux/arm64）" ;;
  esac
  [ "$(uname -s)" = "Linux" ] || die "install.sh 仅支持 Linux（macOS/Windows 请走 Docker 或手动部署）"
}

# ── 安装 ────────────────────────────────────────────────────────

# 预装数据库备份工具（在线更新前强制 DB 备份依赖；缺失更新会被 fail-closed 中止）
ensure_backup_tools() {
  command -v pg_dump >/dev/null || apt-get install -y postgresql-client >/dev/null 2>&1 \
    && c_green "pg_dump 就绪" || c_yellow "pg_dump 未就绪：PG 站点在线更新前需 apt install postgresql-client"
  command -v mysqldump >/dev/null || apt-get install -y default-mysql-client >/dev/null 2>&1 || true
}

do_install() {
  require_root
  detect_arch >/dev/null
  local arch; arch="$(detect_arch)"
  c_yellow "=== ZCard 2.0 安装（linux/$arch）==="

  ensure_backup_tools
  # 1. 二进制：优先本地 ./zcard（离线安装），否则下载 latest release
  local src_bin=""
  if [ -x "./zcard" ] && [ ! -d "./zcard" ]; then
    src_bin="$(pwd)/zcard"; c_green "使用本地二进制: $src_bin"
  else
    command -v curl >/dev/null || die "缺少 curl（apt/yum 安装后重试）"
    local tmp_bin="/tmp/zcard-install-$$"
    c_yellow "下载最新版本 → $DOWNLOAD_BASE/zcard-linux-$arch"
    curl -fSL --retry 3 -o "$tmp_bin" "$DOWNLOAD_BASE/zcard-linux-$arch" \
      || die "下载失败（也可将 zcard 二进制放到脚本同目录后重跑离线安装）"
    src_bin="$tmp_bin"
  fi

  # 2. 目录 + 系统用户（幂等：已存在即跳过）
  id -u "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --home "$INSTALL_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
  mkdir -p "$INSTALL_DIR"/{configs,data,uploads,backups}
  install -m 0755 "$src_bin" "$INSTALL_DIR/zcard"
  [ "${src_bin:0:5}" = "/tmp/" ] && rm -f "$src_bin"

  # 3. 配置（已存在不覆盖——升级重装保配置）
  if [ ! -f "$INSTALL_DIR/configs/config.yaml" ]; then
    cat > "$INSTALL_DIR/configs/config.yaml" << EOF
# ZCard 2.0 部署配置（install.sh 生成；业务开关在后台设置中心）
server:
  http:
    addr: 127.0.0.1:${HTTP_PORT}
    timeout: 30s
  grpc:
    addr: 127.0.0.1:${HTTP_PORT}1
    timeout: 30s
  migrate_on_start: true
data:
  database:
    driver: sqlite
    source: file:${INSTALL_DIR}/data/zcard.db
log:
  level: info
EOF
  fi
  chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"

  # 4. systemd 两件套（zcard + OnFailure 自动回滚单元）
  write_units

  # 5. Nginx（有则配置，无则提示端口直连）
  setup_nginx

  systemctl daemon-reload
  systemctl enable --now "$UNIT_NAME.service" >/dev/null 2>&1 || systemctl restart "$UNIT_NAME.service"

  c_green "=== 安装完成 ==="
  sleep 2
  systemctl is-active --quiet "$UNIT_NAME.service" && c_green "服务运行中: systemctl status $UNIT_NAME" \
    || c_red "服务未就绪: journalctl -u $UNIT_NAME -e 排查"
  c_yellow "下一步: 浏览器打开 http://<服务器IP>:8000/install 完成安装向导（Nginx 配好后走 80/443）"
  c_yellow "管理菜单: zcard-manager"
}

write_units() {
  cat > "/etc/systemd/system/$UNIT_NAME.service" << EOF
[Unit]
Description=ZCard 2.0 (single binary)
After=network-online.target
Wants=network-online.target
# 启动失败（含更新后新版本起不来且自身回滚代码未执行到的极端情形）
# 触发回滚单元；随机崩溃因无 pending 态为 no-op，不误伤
OnFailure=$UNIT_NAME-rollback.service
StartLimitIntervalSec=0

[Service]
Type=simple
User=$SERVICE_USER
# 在线更新重启三分支探测标记（）
Environment=ZCARD_SUPERVISOR=systemd
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/zcard serve -conf $INSTALL_DIR/configs
Restart=always
RestartSec=3
TimeoutStopSec=30
# 加固
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$INSTALL_DIR
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
  cat > "/etc/systemd/system/$UNIT_NAME-rollback.service" << EOF
[Unit]
Description=ZCard update auto-rollback (OnFailure)

[Service]
Type=oneshot
ExecStart=$INSTALL_DIR/zcard self-update --rollback -conf $INSTALL_DIR/configs
EOF
}

setup_nginx() {
  command -v nginx >/dev/null || { c_yellow "未检测到 Nginx——可直连 :$HTTP_PORT，或稍后经菜单「配置域名」补装"; return; }
  local conf="/etc/nginx/sites-available/zcard.conf"
  [ -d /etc/nginx/sites-available ] || conf="/etc/nginx/conf.d/zcard.conf"
  [ -f "$conf" ] && { c_yellow "Nginx 站点已存在，跳过（$conf）"; return; }
  cat > "$conf" << EOF
server {
    listen 80;
    server_name _;
    client_max_body_size 64m;   # 媒体上传

    location / {
        proxy_pass http://127.0.0.1:${HTTP_PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
  # sites-enabled 软链（Debian 系）
  if [ -d /etc/nginx/sites-enabled ] && [ ! -e "/etc/nginx/sites-enabled/zcard.conf" ]; then
    ln -s "$conf" /etc/nginx/sites-enabled/zcard.conf
  fi
  nginx -t >/dev/null 2>&1 && systemctl reload nginx && c_green "Nginx 已配置并重载" \
    || c_yellow "Nginx 配置校验失败，请检查 $conf"
}

# ── 菜单管理（zcard-manager）────────────────────────────────────

do_manage() {
  while true; do
    echo ""
    c_yellow "===== ZCard 管理菜单 ====="
    echo "  1) 服务状态        2) 查看日志       3) 重启服务"
    echo "  4) 检查/在线更新   5) 配置域名+HTTPS 6) 重置管理员密码"
    echo "  7) 服务自检(/health)                 0) 退出"
    read -r -p "选择: " choice
    case "$choice" in
      1) systemctl status "$UNIT_NAME" --no-pager || true ;;
      2) journalctl -u "$UNIT_NAME" -n 100 --no-pager -f ;;
      3) systemctl restart "$UNIT_NAME" && c_green "已重启" ;;
      4)
        su -s /bin/sh "$SERVICE_USER" -c "$INSTALL_DIR/zcard self-update -url $UPDATE_API -conf $INSTALL_DIR/configs" || true
        read -r -p "按回车返回..."
        ;;
      5)
        read -r -p "绑定域名（如 shop.example.com）: " domain
        [ -n "$domain" ] && configure_domain "$domain" || true
        ;;
      6)
        su -s /bin/sh "$SERVICE_USER" -c "$INSTALL_DIR/zcard admin reset-password -conf $INSTALL_DIR/configs" || true
        read -r -p "按回车返回..."
        ;;
      7)
        curl -fsS "http://127.0.0.1:${HTTP_PORT}/health" && echo "" || c_red "健康检查失败"
        read -r -p "按回车返回..."
        ;;
      0) exit 0 ;;
      *) c_red "无效选择" ;;
    esac
  done
}

configure_domain() {
  local domain="$1"
  command -v nginx >/dev/null || { apt-get install -y nginx || die "Nginx 安装失败"; }
  local conf="/etc/nginx/sites-available/zcard.conf"
  [ -d /etc/nginx/sites-available ] || conf="/etc/nginx/conf.d/zcard.conf"
  cat > "$conf" << EOF
server {
    listen 80;
    server_name ${domain};
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:${HTTP_PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
  if [ -d /etc/nginx/sites-enabled ]; then
    ln -sf "$conf" /etc/nginx/sites-enabled/zcard.conf
  fi
  nginx -t && systemctl reload nginx
  c_green "Nginx 已绑定 $domain（HTTP）"
  # HTTPS：certbot --nginx 自动改写 80 站点为 301 + 443
  if command -v certbot >/dev/null; then
    certbot --nginx -d "$domain" --non-interactive --agree-tos -m "admin@${domain#*.}" \
      && c_green "HTTPS 已就绪" || c_yellow "certbot 签发失败（检查 DNS 解析与 80 端口可达后重试）"
  else
    c_yellow "未安装 certbot：apt install certbot python3-certbot-nginx 后重跑本菜单项"
  fi
}

do_uninstall() {
  require_root
  read -r -p "确认卸载 ZCard？（保留 $INSTALL_DIR 数据目录，输入 yes 确认）: " ok
  [ "$ok" = "yes" ] || die "已取消"
  systemctl disable --now "$UNIT_NAME.service" "$UNIT_NAME-rollback.service" 2>/dev/null || true
  rm -f "/etc/systemd/system/$UNIT_NAME.service" "/etc/systemd/system/$UNIT_NAME-rollback.service"
  rm -f /etc/nginx/sites-enabled/zcard.conf /etc/nginx/sites-available/zcard.conf /etc/nginx/conf.d/zcard.conf
  systemctl daemon-reload
  c_green "已卸载服务与 Nginx 站点；数据保留在 $INSTALL_DIR（确认不再需要可手动删除）"
}

# ── 入口 ────────────────────────────────────────────────────────

main() {
  local cmd="${1:-install}"
  case "$cmd" in
    install)  do_install ;;
    manage)   do_manage ;;
    uninstall) do_uninstall ;;
    *) echo "用法: $0 [install|manage|uninstall]"; exit 2 ;;
  esac
}

# 经 zcard-manager 符号链接调用时进菜单
if [ "$(basename "$0")" = "zcard-manager" ]; then
  require_root
  do_manage
else
  main "$@"
fi
