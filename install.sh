#!/usr/bin/env bash
set -euo pipefail

REPO="block-0N/ghfast"
BIN_NAME="ghfast"
INSTALL_DIR="${GHFAST_INSTALL_DIR:-$HOME/.local/bin}"

if [ -t 1 ]; then
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
    BLUE='\033[0;34m'; NC='\033[0m'
else
    RED=''; GREEN=''; YELLOW=''; BLUE=''; NC=''
fi

info() { printf "${BLUE}==>${NC} %s\n" "$*"; }
ok()   { printf "${GREEN}✓${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}!${NC} %s\n" "$*"; }
err()  { printf "${RED}✗${NC} %s\n" "$*" >&2; }

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *) echo "unknown" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "unknown" ;;
    esac
}

OS=$(detect_os)
ARCH=$(detect_arch)

if [ "$OS" = "unknown" ] || [ "$ARCH" = "unknown" ]; then
    err "不支持的平台: $(uname -s)/$(uname -m)"
    exit 1
fi

ASSET_NAME="${BIN_NAME}-${OS}-${ARCH}"

case "$ASSET_NAME" in
    ghfast-linux-amd64|ghfast-darwin-amd64|ghfast-darwin-arm64)
        ;;
    *)
        err "暂不支持 $OS/$ARCH"
        echo "  当前可用: linux-amd64, darwin-amd64, darwin-arm64"
        echo "  如需其他平台，请到 ${REPO}/issues 反馈"
        exit 1
        ;;
esac

info "平台: $OS/$ARCH"

if ! command -v curl >/dev/null 2>&1; then
    err "未找到 curl，请先安装 curl 后重试"
    exit 1
fi

info "查询最新版本..."
API_URL="https://api.github.com/repos/${REPO}/releases/latest"
RELEASE_JSON=$(curl -fsSL "$API_URL" 2>/dev/null) || {
    err "无法访问 GitHub API"
    echo "  如果使用代理，请设置: export HTTPS_PROXY=http://127.0.0.1:7890"
    exit 1
}

# 解析 JSON：优先用 python3，没有则用 grep 兜底
parse_json_field() {
    local field="$1"
    if command -v python3 >/dev/null 2>&1; then
        printf '%s' "$RELEASE_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
if '$field' == 'tag':
    print(data.get('tag_name', ''))
elif '$field' == 'asset_id':
    for a in data.get('assets', []):
        if a.get('name') == '$ASSET_NAME':
            print(a.get('id', ''))
            break
" 2>/dev/null
    else
        if [ "$field" = "tag" ]; then
            printf '%s' "$RELEASE_JSON" \
                | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
                | head -1 | sed 's/.*"\([^"]*\)"$/\1/'
        fi
    fi
}

TAG=$(parse_json_field tag)
if [ -z "$TAG" ]; then
    err "无法解析版本号"
    exit 1
fi

ASSET_ID=$(parse_json_field asset_id)
if [ -z "$ASSET_ID" ]; then
    err "在版本 $TAG 中找不到文件: $ASSET_NAME"
    if ! command -v python3 >/dev/null 2>&1; then
        echo "  提示: 安装 python3 可提高解析可靠性"
    fi
    exit 1
fi

ok "最新版本: $TAG"

mkdir -p "$INSTALL_DIR"

TMP_FILE=$(mktemp)
cleanup() { rm -f "$TMP_FILE"; }
trap cleanup EXIT

# 通过 API asset 端点下载，绕开被墙的 github.com
DOWNLOAD_URL="https://api.github.com/repos/${REPO}/releases/assets/${ASSET_ID}"
info "下载 $ASSET_NAME ..."

if ! curl -fL \
    -H "Accept: application/octet-stream" \
    --connect-timeout 15 \
    --retry 3 \
    --retry-delay 2 \
    -o "$TMP_FILE" \
    "$DOWNLOAD_URL"; then
    err "下载失败"
    exit 1
fi

SIZE=$(wc -c < "$TMP_FILE" | tr -d ' ')
if [ "$SIZE" -lt 1000000 ]; then
    err "下载的文件异常（只有 $SIZE 字节），可能链接已过期"
    exit 1
fi

chmod +x "$TMP_FILE"
mv "$TMP_FILE" "$INSTALL_DIR/$BIN_NAME"
trap - EXIT

ok "已安装到 $INSTALL_DIR/$BIN_NAME ($((SIZE / 1024 / 1024)) MB)"

# PATH 检查
case ":$PATH:" in
    *":$INSTALL_DIR:"*)
        ok "$INSTALL_DIR 已在 PATH 中"
        ;;
    *)
        echo ""
        warn "$INSTALL_DIR 不在 PATH 中"
        echo ""
        echo "  将下面这行加到你的 shell 配置里："
        echo ""
        case "${SHELL:-}" in
            */zsh)
                printf "    echo 'export PATH=\"%s:\$PATH\"' >> ~/.zshrc\n" "$INSTALL_DIR"
                printf "    source ~/.zshrc\n"
                ;;
            */bash)
                printf "    echo 'export PATH=\"%s:\$PATH\"' >> ~/.bashrc\n" "$INSTALL_DIR"
                printf "    source ~/.bashrc\n"
                ;;
            *)
                printf "    export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
                ;;
        esac
        echo ""
        ;;
esac

echo ""
if command -v "$BIN_NAME" >/dev/null 2>&1; then
    ok "安装完成，运行 '$BIN_NAME' 查看用法"
else
    ok "安装完成，重开终端后运行 '$BIN_NAME'"
fi