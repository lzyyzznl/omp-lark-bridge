#!/bin/sh
# omp-lark-bridge 一键安装：下载匹配本机平台的预编译二进制并注册到 PATH。
# 任意新机器装完技能后，agent 若报「omp-lark-bridge 路径不存在」，先跑本脚本。
# 来源顺序：1) GitHub Releases 预编译资产（gh 已认证）；2) go install（私有模块需凭据）。
# 用法：sh install.sh [--force]
set -u

APP=omp-lark-bridge
OWNER_REPO=lzyyzznl/omp-lark-bridge

# --- 平台映射 ---------------------------------------------------------------
os=$(uname -s)
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)
    echo "install.sh: 暂不支持平台 $(uname -s)（可 make build 或 go install）" >&2
    exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    echo "install.sh: 暂不支持架构 $arch" >&2
    exit 1 ;;
esac

# --- 已在 PATH 则跳过 --------------------------------------------------------
if command -v "$APP" >/dev/null 2>&1 && [ "${1:-}" != "--force" ]; then
  echo "✓ $APP 已在 PATH：$(command -v "$APP")"
  exit 0
fi

# --- 目标 bin 目录（~/.local/bin 优先，其次 ~/bin） -------------------------
for cand in "$HOME/.local/bin" "$HOME/bin"; do
  if [ -d "$cand" ] || mkdir -p "$cand" 2>/dev/null; then
    BIN_DIR=$cand
    break
  fi
done
: "${BIN_DIR:?无法创建用户 bin 目录（~/.local/bin 与 ~/bin 均失败）}"

installed=0

# --- 来源 1：GitHub Releases 预编译资产 -------------------------------------
if command -v gh >/dev/null 2>&1; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  if gh release download --repo "$OWNER_REPO" \
      --pattern "omp-lark-bridge-*-$os-$arch.tar.gz" --dir "$tmp" 2>/dev/null; then
    mkdir -p "$tmp/src"
    if tar xzf "$(ls "$tmp"/*.tar.gz 2>/dev/null | head -n 1)" -C "$tmp/src" 2>/dev/null; then
      bin=$(find "$tmp/src" -maxdepth 1 -type f -name 'omp-lark-bridge-*' | head -n 1)
      chmod +x "$bin"
      if [ -n "$bin" ] && mv -f "$bin" "$BIN_DIR/$APP"; then
        installed=1
      fi
    fi
  fi
fi

# --- 来源 2：go install（私有模块需已配置凭据） ----------------------------
if [ "$installed" -eq 0 ] && command -v go >/dev/null 2>&1; then
  if GOBIN="$BIN_DIR" go install "github.com/$OWNER_REPO/cmd/$APP@latest" 2>/dev/null; then
    installed=1
  fi
fi

if [ "$installed" -eq 0 ]; then
  echo "✗ 未能自动安装 $APP。手动方式：" >&2
  echo "  1) gh auth status 确保已登录后重跑本脚本；或" >&2
  echo "  2) 克隆源码 make build，把产物软链到 ~/.local/bin；或" >&2
  echo "  3) go install github.com/$OWNER_REPO/cmd/$APP@latest" >&2
  exit 1
fi

# --- 注册 PATH（写入 login + interactive 都读的 rc） ------------------------
case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *)
    line="export PATH=\"$BIN_DIR:\$PATH\""
    for rcfile in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
      touch "$rcfile"
      grep -qF "export PATH=\"$BIN_DIR" "$rcfile" 2>/dev/null || \
        printf '\n# added by %s install.sh\n%s\n' "$APP" "$line" >> "$rcfile"
    done
    echo "已把 $BIN_DIR 加入 PATH（写入 ~/.profile / ~/.bashrc / ~/.zshrc）；新 shell 生效。"
    ;;
esac

echo "✓ 已安装 $APP：$BIN_DIR/$APP"
"$BIN_DIR/$APP" --version
