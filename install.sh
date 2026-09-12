#!/bin/bash

# Node Content Installation Script
# For private repositories, export GITHUB_TOKEN or pass --github-token.

set -e
umask 077

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Defaults
UNINSTALL=false
DATABASE_URL=""
REDIS_URL=""
DOMAIN_STATIC=""
LOG_PATH=""
PORT="8082"
ENV_FILE=""
GITHUB_USER="${GITHUB_USER:-}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
DOMAINS=()            # --domain d1 d2 ... (ว่าง = catch-all รับทุกโดเมน)
APP_HOST="localhost"  # --app-host 10.0.0.1 (โหมด nginx ชี้ app คนละเครื่อง)
INSTALL_APP=false
INSTALL_NGINX=false

APP_NAME="node-content"
APP_DIR="/opt/$APP_NAME"
SERVICE_NAME="node-content"
GITHUB_REPO="avxtube/node-content"
RELEASES_URL="https://github.com/$GITHUB_REPO/releases/latest/download"
GITHUB_API="https://api.github.com/repos/$GITHUB_REPO"

print_status()  { echo -e "${GREEN}[INFO]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }

# ชื่อตัวแปร nginx ห้ามมี "-" — node-content → node_proxy
CONN_VAR="${APP_NAME//-/_}_conn"

# map เลือกค่า header Connection ตามคำขอแต่ละอัน
#
# keepalive ไปยัง upstream จะทำงานก็ต่อเมื่อ Connection เป็นค่าว่าง แต่
# WebSocket ต้องการ "upgrade" — ของเดิม hardcode 'upgrade' ไว้ตายตัว
# keepalive 32 ที่ตั้งไว้จึงไม่เคยถูกใช้เลย เปิด TCP ใหม่ทุก request
nginx_conn_map() {
    cat <<MAP
map \$http_upgrade \$${CONN_VAR} {
    default upgrade;
    ''      '';
}
MAP
}

# Parse args
while [[ $# -gt 0 ]]; do
    case $1 in
        --database-url|--mongodb-uri|--redis-url|--domain-static|--log-path|--env-file|--github-user|--github-token|--port|--app-host)
            if [[ $# -lt 2 || -z "$2" || "$2" == --* ]]; then print_error "$1 requires a value"; exit 1; fi ;;
    esac
    case $1 in
        --uninstall)         UNINSTALL=true; shift ;;
        --app)               INSTALL_APP=true; shift ;;
        --nginx)             INSTALL_NGINX=true; shift ;;
        --database-url)      DATABASE_URL="$2"; shift 2 ;;
        --mongodb-uri)       DATABASE_URL="$2"; shift 2 ;; # alias เดิม
        --redis-url)         REDIS_URL="$2"; shift 2 ;;
        --domain-static)     DOMAIN_STATIC="$2"; shift 2 ;;
        --log-path)          LOG_PATH="$2"; shift 2 ;;
        --env-file)          ENV_FILE="$2"; shift 2 ;;
        --github-user)
            if [[ $# -lt 2 || -z "$2" ]]; then print_error "--github-user requires a value"; exit 1; fi
            GITHUB_USER="$2"; shift 2 ;;
        --github-token)
            if [[ $# -lt 2 || -z "$2" ]]; then print_error "--github-token requires a value"; exit 1; fi
            GITHUB_TOKEN="$2"; shift 2 ;;
        --port)              PORT="$2"; shift 2 ;;
        --app-host)          APP_HOST="$2"; shift 2 ;;
        -d|--domain)
            # เก็บทุก arg ต่อจากนี้ที่ไม่ใช่ flag เป็นรายชื่อโดเมน
            shift
            if [[ $# -eq 0 || "$1" == -* ]]; then print_error "--domain requires a domain"; exit 1; fi
            while [[ $# -gt 0 && "$1" != -* ]]; do
                DOMAINS+=("$1"); shift
            done ;;
        -h|--help)
            echo "Node Content Installer"
            echo ""
            echo "Usage: curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo -E bash -s -- [OPTIONS]"
            echo ""
            echo "Modes (ไม่ระบุ = ทำทั้งคู่):"
            echo "  --app                ติดตั้ง app + systemd อย่างเดียว"
            echo "  --nginx              ตั้ง nginx อย่างเดียว (app ติดตั้งแล้ว/อยู่เครื่องอื่น)"
            echo ""
            echo "Options:"
            echo "  --uninstall          Uninstall completely (app + nginx vhost)"
            echo "  --database-url URI   MongoDB connection string (DATABASE_URL)"
            echo "  --mongodb-uri URI    Alias ของ --database-url"
            echo "  --redis-url URL      Redis URL (optional)"
            echo "  --domain-static HOST Static domain fallback (optional)"
            echo "  --log-path PATH      Rotating log path (optional)"
            echo "  --env-file FILE      Existing environment file containing DATABASE_URL"
            echo "  --github-user USER   GitHub username used to fetch a private installer/release"
            echo "  --github-token TOKEN Personal access token used only during installation"
            echo "  --port PORT          HTTP port (default: 8082)"
            echo "  -d, --domain D1 D2   โดเมนเฉพาะ (ไม่ระบุ = catch-all รับทุกโดเมน)"
            echo "  --app-host HOST      ให้ nginx ชี้ app เครื่องอื่น (default: localhost)"
            echo "  -h, --help           Show this help"
            echo ""
            echo "Examples:"
            echo "  # ติดตั้งครบ (app + nginx catch-all)"
            echo "  curl -fsSL ... | sudo -E bash -s -- --database-url \"mongodb+srv://...\""
            echo ""
            echo "  # ติดตั้งพร้อมโดเมนเฉพาะ"
            echo "  curl -fsSL ... | sudo -E bash -s -- --database-url \"...\" --domain embed.example.com cdn.example.com"
            echo ""
            echo "  # App เครื่อง A / Nginx เครื่อง B"
            echo "  A: curl -fsSL ... | sudo -E bash -s -- --app --database-url \"...\""
            echo "  B: curl -fsSL ... | sudo -E bash -s -- --nginx --app-host 10.0.0.1"
            exit 0 ;;
        *)
            print_error "Unknown option: $1"; exit 1 ;;
    esac
done

# ไม่ระบุโหมด = ทำทั้ง app + nginx (เหมือน installer เดิม)
if [ "$INSTALL_APP" = false ] && [ "$INSTALL_NGINX" = false ]; then
    INSTALL_APP=true
    INSTALL_NGINX=true
fi

if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    print_error "Invalid port: $PORT"
    exit 1
fi
if [ "$APP_HOST" = "0.0.0.0" ] || [ "$APP_HOST" = "::" ]; then
    print_warning "--app-host is a connection destination, not the app listen address; using 127.0.0.1"
    APP_HOST="127.0.0.1"
fi
if ! [[ "$APP_HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*$ || "$APP_HOST" =~ ^\[[A-Fa-f0-9:]+\]$ ]]; then
    print_error "Invalid app host"
    exit 1
fi
if [[ "$DATABASE_URL" == *[$'\r\n']* || "$DATABASE_URL" == *'"'* || "$DATABASE_URL" == *"'"* || "$DATABASE_URL" == *'\'* ]]; then
    print_error "Percent-encode special characters in the database URI"
    exit 1
fi
for env_value in "$REDIS_URL" "$DOMAIN_STATIC" "$LOG_PATH"; do
    if [[ "$env_value" == *[$'\r\n']* ]]; then print_error "Environment values cannot contain newlines"; exit 1; fi
done
if [[ -n "$DATABASE_URL" && "$DATABASE_URL" != mongodb://* && "$DATABASE_URL" != mongodb+srv://* ]]; then
    print_error "Invalid MongoDB URI"
    exit 1
fi
if [[ "$GITHUB_TOKEN" == *[$'\r\n']* ]]; then print_error "Invalid GitHub token"; exit 1; fi
if [[ -n "$ENV_FILE" && -n "$DATABASE_URL" ]]; then print_error "Choose --env-file or --database-url"; exit 1; fi
for domain in "${DOMAINS[@]}"; do
    if ! [[ "$domain" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]; then
        print_error "Invalid domain: $domain"
        exit 1
    fi
done
if [ "$UNINSTALL" = false ] && [ -n "$ENV_FILE" ] && [ ! -r "$ENV_FILE" ]; then
    print_error "Environment file is not readable: $ENV_FILE"
    exit 1
fi
if [ -z "$ENV_FILE" ] && [ -z "$DATABASE_URL" ] && [ ! -s "$APP_DIR/.env" ] && [ -s /etc/node-content.env ]; then
    ENV_FILE=/etc/node-content.env
fi
if [ "$UNINSTALL" = false ] && [ "$INSTALL_APP" = true ] && [ -z "$ENV_FILE" ] && [ -z "$DATABASE_URL" ] && [ ! -s "$APP_DIR/.env" ]; then
    print_error "Use --env-file FILE or --database-url URI for app installation"
    exit 1
fi

# Check privileges before either installation or removal.
if [ "$(id -u)" -ne 0 ]; then
    print_error "This script must be run as root (use sudo)"
    exit 1
fi
command -v flock >/dev/null || { print_error "Required: flock"; exit 1; }
exec 9>/run/lock/node-content-install.lock
flock -n 9 || { print_error "Another install is running"; exit 1; }

# ─── Uninstall ────────────────────────────────────────────────
if [ "$UNINSTALL" = true ]; then
    print_warning "⚠️  Starting Uninstallation..."
    systemctl stop "${SERVICE_NAME}"    2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ] && rm "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    [ -d "$APP_DIR" ] && rm -rf "$APP_DIR"
    # nginx vhost ของ app นี้ (ถ้ามี)
    NGINX_TOUCHED=false
    if [ -f "/etc/nginx/sites-available/$APP_NAME" ]; then
        rm -f "/etc/nginx/sites-available/$APP_NAME" "/etc/nginx/sites-enabled/$APP_NAME"
        NGINX_TOUCHED=true
    fi
    # catch-all เก่าที่เคยเขียนทับ default ไว้ — ลบเฉพาะไฟล์ที่เป็นของเรา
    if [ -f /etc/nginx/sites-available/default ] &&
        head -1 /etc/nginx/sites-available/default | grep -q "^# $APP_NAME: catch-all"; then
        rm -f /etc/nginx/sites-available/default /etc/nginx/sites-enabled/default
        NGINX_TOUCHED=true
    fi
    if [ "$NGINX_TOUCHED" = true ]; then
        command -v nginx &>/dev/null && nginx -t 2>/dev/null && systemctl reload nginx || true
    fi
    print_status "✅ Uninstalled successfully!"
    exit 0
fi

print_status "🚀 Starting Installation... (app=$INSTALL_APP nginx=$INSTALL_NGINX)"

# ═── App ──────────────────────────────────────────────────────
if [ "$INSTALL_APP" = true ]; then

# ─── System Dependencies ──────────────────────────────────────
print_status "Installing system dependencies (curl, jq)..."
if command -v apt-get &>/dev/null; then
    apt-get -o DPkg::Lock::Timeout=300 update -qq
    apt-get -o DPkg::Lock::Timeout=300 install -y -qq ca-certificates curl jq
elif command -v yum &>/dev/null; then
    yum install -y ca-certificates curl jq
elif command -v dnf &>/dev/null; then
    dnf install -y ca-certificates curl jq
fi



# ─── Create app directory ─────────────────────────────────────
print_status "Creating app directory: $APP_DIR"
mkdir -p "$APP_DIR"
cd "$APP_DIR"

# ─── Download binary ──────────────────────────────────────────
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    BINARY="linux"
elif [ "$ARCH" = "aarch64" ]; then
    BINARY="linux-arm64"
else
    print_error "Unsupported architecture: $ARCH"
    exit 1
fi

print_status "Downloading binary ($BINARY) from latest release..."
DOWNLOAD_PATH=$(mktemp "$APP_DIR/.node-content-download.XXXXXX")
trap 'rm -f "$DOWNLOAD_PATH"' EXIT
if [ -n "${GITHUB_TOKEN:-}" ]; then
    if [ -n "$GITHUB_USER" ]; then
        print_status "Using GitHub authentication for: $GITHUB_USER"
    else
        print_status "Using GitHub token authentication"
    fi
    RELEASE_JSON=$(curl -fsSL \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "$GITHUB_API/releases/latest")
    ASSET_URL=$(printf '%s' "$RELEASE_JSON" | jq -er --arg name "$BINARY" '.assets[] | select(.name == $name) | .url' | head -1)
    if [ -z "$ASSET_URL" ]; then
        print_error "Release asset not found: $BINARY"
        exit 1
    fi
    curl -fsSL \
        -H "Accept: application/octet-stream" \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "$ASSET_URL" -o "$DOWNLOAD_PATH"
else
    print_warning "No GitHub token supplied; private release downloads will return 404"
    curl -fsSL "$RELEASES_URL/$BINARY" -o "$DOWNLOAD_PATH"
fi
if [ ! -s "$DOWNLOAD_PATH" ]; then
    print_error "Downloaded release asset is empty"
    exit 1
fi
if [ "$(od -An -tx1 -N4 "$DOWNLOAD_PATH" | tr -d ' \n')" != 7f454c46 ]; then
    print_error "Release asset is not a Linux ELF binary"
    exit 1
fi
if [ -f "$APP_DIR/$APP_NAME" ]; then cp -p "$APP_DIR/$APP_NAME" "$APP_DIR/$APP_NAME.previous"; fi
install -m 755 "$DOWNLOAD_PATH" "$APP_DIR/$APP_NAME.new"
rm -f "$DOWNLOAD_PATH"
trap - EXIT

# ─── Create .env ─────────────────────────────────────────────
print_status "Creating .env file..."
ENV_DEST="$APP_DIR/.env"
if [ -n "$ENV_FILE" ]; then
    TEMP_ENV=$(mktemp)
    grep -E '^(DATABASE_URL|REDIS_URL|DOMAIN_STATIC|LOG_PATH)=' "$ENV_FILE" > "$TEMP_ENV" || true
    install -m 600 "$TEMP_ENV" "$ENV_DEST"
    rm -f "$TEMP_ENV"
elif [ -n "$DATABASE_URL" ]; then
    umask 077
    printf 'DATABASE_URL=%s\n' "$DATABASE_URL" > "$ENV_DEST"
elif [ ! -s "$ENV_DEST" ]; then
    print_error "Use --env-file FILE or --database-url URI for app installation"
    exit 1
else
    print_warning "No database option supplied; preserving existing $ENV_DEST"
	TEMP_ENV=$(mktemp)
	grep -E '^(DATABASE_URL|REDIS_URL|DOMAIN_STATIC|LOG_PATH)=' "$ENV_DEST" > "$TEMP_ENV" || true
	install -m 600 "$TEMP_ENV" "$ENV_DEST"
	rm -f "$TEMP_ENV"
fi
set_env_value() {
    local key="$1" value="$2" temp_env
    temp_env=$(mktemp)
    grep -v "^${key}=" "$ENV_DEST" > "$temp_env" || true
    printf '%s=%s\n' "$key" "$value" >> "$temp_env"
    install -m 600 "$temp_env" "$ENV_DEST"
    rm -f "$temp_env"
}
set_env_value PORT "$PORT"
if [ -n "$REDIS_URL" ]; then set_env_value REDIS_URL "$REDIS_URL"; fi
if [ -n "$DOMAIN_STATIC" ]; then set_env_value DOMAIN_STATIC "$DOMAIN_STATIC"; fi
if [ -n "$LOG_PATH" ]; then set_env_value LOG_PATH "$LOG_PATH"; fi
chmod 600 "$ENV_DEST"
if ! grep -qE '^DATABASE_URL=.+$' "$ENV_DEST"; then
    print_error "Environment must contain DATABASE_URL"
    exit 1
fi

# Keep compatibility with the previous installer's environment file.
getent group node-content >/dev/null || groupadd --system node-content
id node-content >/dev/null 2>&1 || useradd --system --gid node-content --home-dir /var/lib/node-content --shell /usr/sbin/nologin node-content
install -d -o node-content -g node-content -m 700 /var/lib/node-content /var/lib/node-content/.cached
install -d -o node-content -g node-content -m 750 "$APP_DIR/conf"
chown root:node-content "$ENV_DEST"
chmod 640 "$ENV_DEST"
chmod 755 "$APP_DIR"

# ─── Systemd service ──────────────────────────────────────────
print_status "Creating systemd service..."
cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=AVXTube Content Delivery
After=network.target

[Service]
Type=simple
User=node-content
Group=node-content
WorkingDirectory=/var/lib/node-content
ExecStart=$APP_DIR/$APP_NAME
Restart=on-failure
RestartSec=5
EnvironmentFile=$APP_DIR/.env
TimeoutStopSec=20
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/var/lib/node-content $APP_DIR/conf

[Install]
WantedBy=multi-user.target
EOF

# ─── Enable & start ───────────────────────────────────────────
mv -f "$APP_DIR/$APP_NAME.new" "$APP_DIR/$APP_NAME"
systemctl daemon-reload
systemctl enable ${SERVICE_NAME}
systemctl restart ${SERVICE_NAME}
READY=false
for ((attempt=0;attempt<30;attempt++)); do
    if curl --noproxy '*' -fsS --max-time 2 "http://127.0.0.1:$PORT/ready" >/dev/null; then READY=true; break; fi
    sleep 1
done
if [ "$READY" = false ]; then
    print_error "Readiness failed; inspect journalctl -u $SERVICE_NAME"
    if [ -f "$APP_DIR/$APP_NAME.previous" ]; then
        cp -p "$APP_DIR/$APP_NAME.previous" "$APP_DIR/$APP_NAME.rollback"
        mv -f "$APP_DIR/$APP_NAME.rollback" "$APP_DIR/$APP_NAME"
        systemctl restart "$SERVICE_NAME"
    else
        systemctl stop "$SERVICE_NAME"
    fi
    exit 1
fi

fi # INSTALL_APP

# ═── Nginx ────────────────────────────────────────────────────
if [ "$INSTALL_NGINX" = true ]; then
    print_status "Configuring Nginx..."

    if ! command -v nginx &>/dev/null; then
        print_status "Installing Nginx..."
        apt-get -o DPkg::Lock::Timeout=300 update -qq
        apt-get -o DPkg::Lock::Timeout=300 install -y nginx
        systemctl enable nginx
    fi
    mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled

    if [ "${#DOMAINS[@]}" -eq 0 ]; then
        # ── Catch-all: รับทุกโดเมน ────────────────────────────
        #
        # เขียนลงไฟล์ของตัวเอง ไม่แตะ sites-available/default เพราะไฟล์นั้น
        # อาจเป็นของ app อื่นบนเครื่องเดียวกัน (เช่น player-node) — ของเดิม
        # เขียนทับทำให้ vhost ของเขาหายเงียบๆ
        #
        # ถ้ามี vhost อื่นจอง default_server อยู่แล้ว เราจะไม่ประกาศซ้ำ
        # (nginx จะ error "duplicate default server" แล้ว reload ไม่ผ่านทั้งเครื่อง)
        DEFAULT_OWNER=""
        if [ -d /etc/nginx/sites-enabled ]; then
            DEFAULT_OWNER=$(grep -rls "default_server" /etc/nginx/sites-enabled/ 2>/dev/null \
                | grep -v "/${APP_NAME}$" | head -1)
        fi

        LISTEN_80="listen 80 default_server;"
        LISTEN_V6="listen [::]:80 default_server;"
        if [ -n "$DEFAULT_OWNER" ]; then
            print_warning "มี vhost อื่นจอง default_server อยู่แล้ว: $DEFAULT_OWNER"
            print_warning "จะไม่ประกาศ default_server ซ้ำ — โดเมนที่ไม่ตรง vhost ไหนจะไปที่ตัวนั้นแทน"
            print_warning "ถ้าต้องการให้ $APP_NAME รับทุกโดเมน ให้ระบุ --domain หรือย้าย vhost นั้นออก"
            LISTEN_80="listen 80;"
            LISTEN_V6="listen [::]:80;"
        fi

        print_status "No --domain → catch-all (accept ALL domains) → $APP_HOST:$PORT"
        cat > /etc/nginx/sites-available/$APP_NAME <<EOF
# $APP_NAME: catch-all — accepts every domain
$(nginx_conn_map)

upstream $APP_NAME {
    server $APP_HOST:$PORT;
    keepalive          32;
    keepalive_timeout  60s;
    keepalive_requests 1000;
}

server {
    $LISTEN_80
    $LISTEN_V6
    server_name _;

    proxy_buffering         off;
    proxy_request_buffering off;

    location / {
        proxy_pass         http://$APP_NAME;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade           \$http_upgrade;
        proxy_set_header   Connection        \$${CONN_VAR};
        proxy_set_header   Host              \$host;
        proxy_set_header   X-Real-IP         \$remote_addr;
        proxy_set_header   X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
EOF
        ln -sf /etc/nginx/sites-available/$APP_NAME /etc/nginx/sites-enabled/$APP_NAME

        # เวอร์ชันก่อนหน้าเขียน catch-all ทับ sites-available/default ไว้
        # — ถ้าเจอไฟล์ที่เป็นของเรา (มี marker) ให้เก็บกวาดทิ้ง ไฟล์ของคนอื่นไม่แตะ
        if [ -f /etc/nginx/sites-available/default ] &&
            head -1 /etc/nginx/sites-available/default | grep -q "^# $APP_NAME: catch-all"; then
            print_warning "พบ catch-all เก่าของ $APP_NAME ใน sites-available/default — ลบทิ้ง"
            rm -f /etc/nginx/sites-available/default /etc/nginx/sites-enabled/default
        fi
    else
        # ── โดเมนเฉพาะ ────────────────────────────────────────
        SERVER_NAMES="${DOMAINS[*]}"
        print_status "Domains: $SERVER_NAMES → $APP_HOST:$PORT"
        cat > /etc/nginx/sites-available/$APP_NAME <<EOF
$(nginx_conn_map)

upstream $APP_NAME {
    server $APP_HOST:$PORT;
    keepalive          32;
    keepalive_timeout  60s;
    keepalive_requests 1000;
}

server {
    listen 80;
    server_name $SERVER_NAMES;

    proxy_buffering         off;
    proxy_request_buffering off;

    location / {
        proxy_pass         http://$APP_NAME;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade           \$http_upgrade;
        proxy_set_header   Connection        \$${CONN_VAR};
        proxy_set_header   Host              \$host;
        proxy_set_header   X-Real-IP         \$remote_addr;
        proxy_set_header   X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
EOF
        ln -sf /etc/nginx/sites-available/$APP_NAME /etc/nginx/sites-enabled/
    fi

    if nginx -t; then
        if systemctl is-active --quiet nginx; then
            if ! systemctl reload nginx; then
                print_error "❌ Nginx reload failed"
                systemctl status nginx --no-pager -l || true
                journalctl -u nginx -n 30 --no-pager || true
                exit 1
            fi
        else
            if ! systemctl start nginx; then
                print_error "❌ Nginx start failed; checking listeners on ports 80 and 443"
                command -v ss &>/dev/null && ss -ltnp | grep -E ':(80|443)[[:space:]]' || true
                systemctl status nginx --no-pager -l || true
                journalctl -u nginx -n 30 --no-pager || true
                exit 1
            fi
        fi
        print_status "✅ Nginx configured"
    else
        print_error "❌ Nginx configuration test failed."
        exit 1
    fi
fi # INSTALL_NGINX

# ═── Done ─────────────────────────────────────────────────────
sleep 2
echo ""
echo "============================================"
if [ "$INSTALL_APP" = true ]; then
    if systemctl is-active --quiet ${SERVICE_NAME}; then
        print_status "✅ Installation completed successfully!"
    else
        print_warning "Service not running — check logs below"
        journalctl -u "${SERVICE_NAME}" -n 15 --no-pager
    fi
fi
echo "============================================"
echo ""
echo "  Port:       $PORT"
if [ "${#DOMAINS[@]}" -gt 0 ]; then
    echo "  Domains:"
    for d in "${DOMAINS[@]}"; do echo "    • http://$d"; done
elif [ "$INSTALL_NGINX" = true ]; then
    echo "  Domains:    all (catch-all)"
fi
echo ""
echo "  Commands:"
echo "    View logs:  journalctl -u ${SERVICE_NAME} -f"
echo "    Restart:    systemctl restart ${SERVICE_NAME}"
echo "    Health:     curl http://localhost:$PORT/health"
echo "    Uninstall:  curl -fsSL https://raw.githubusercontent.com/$GITHUB_REPO/main/install.sh | sudo bash -s -- --uninstall"
echo "============================================"
