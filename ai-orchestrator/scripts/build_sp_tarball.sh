#!/bin/bash
# scripts/build_sp_tarball.sh
# 从 .venv-312 导出 site-packages 到 bin/sp.tar.gz，并验证关键依赖。
# 未来新增 Python 依赖时，改 requirements.txt 后重新打包，避免遗漏。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ORCH_DIR="$(dirname "$SCRIPT_DIR")"
PY312_LIB="$ORCH_DIR/.venv-312/lib/python3.12"
VENV_SITE="$PY312_LIB/site-packages"
OUTPUT="$ORCH_DIR/bin/sp.tar.gz"

# 关键依赖清单（用于打包后验证）
KEY_DEPS=(
    apscheduler
    langgraph
    crewai
    chromadb
    sentence_transformers
    fastapi
    uvicorn
    pymysql
    minio
    aiosqlite
    tzlocal
)

echo ">>> [1/4] 检查 venv site-packages"
if [ ! -d "$VENV_SITE" ]; then
    echo "ERROR: venv site-packages not found at $VENV_SITE"
    echo "Run: python3.12 -m venv .venv-312 && .venv-312/bin/pip install -r requirements.txt"
    exit 1
fi

echo ">>> [2/4] 打包 sp.tar.gz"
mkdir -p "$ORCH_DIR/bin"
# -C 到 python3.12 目录再打包 site-packages 目录本身 → 顶层为 site-packages/
# （Dockerfile 解压到 /usr/local/lib/python3.12/ 后即为正确的 site-packages 内容）。
# 排除 __pycache__ 与 *.pyc 减小体积；保留 dist-info/egg-info（requirements 解析时需要）。
# 该写法同时兼容 GNU tar 与 macOS bsdtar。
tar czf "$OUTPUT" -C "$PY312_LIB" \
    --exclude='*/__pycache__' \
    --exclude='*.pyc' \
    site-packages

echo ">>> [3/4] 验证关键依赖"
MISSING=0
for dep in "${KEY_DEPS[@]}"; do
    if ! tar tzf "$OUTPUT" | grep -q "site-packages/$dep/"; then
        echo "MISSING: $dep"
        MISSING=$((MISSING + 1))
    fi
done

if [ "$MISSING" -gt 0 ]; then
    echo "ERROR: $MISSING key dependencies missing from sp.tar.gz"
    exit 1
fi

echo ">>> [4/4] 完成"
ls -lh "$OUTPUT"
echo "All ${#KEY_DEPS[@]} key dependencies verified present."
