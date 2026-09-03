#!/usr/bin/env bash
set -euo pipefail

# P1-SUP1: (re)generate the offline wheelhouse + hashed lock for the
# ai-orchestrator production image. Run on a trusted network before image
# builds; results (wheelhouse.sha256, requirements-lock.txt) are committed so
# image builds are fully offline + hash-verified.
# Usage: deploy/scripts/build-wheelhouse.sh [amd64|arm64|both]   (default both)
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
orchestrator="${repo_root}/ai-orchestrator"
cd "${orchestrator}"
mkdir -p wheelhouse

targets=("$@")
if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(amd64 arm64)
fi

# downloader python: a 3.12 interpreter with `packaging` installed (uv/pip managed)
PY="${SUP1_PYTHON:-python3.12}"

for arch in "${targets[@]}"; do
  case "${arch}" in
    amd64) plat="manylinux2014_x86_64 --platform manylinux_2_28_x86_64" ;;
    arm64) plat="manylinux2014_aarch64 --platform manylinux_2_28_aarch64" ;;
    *) echo "unknown arch ${arch} (amd64|arm64)"; exit 1 ;;
  esac
  echo "[wheelhouse] downloading ${arch} (Tsinghua PyPI + pytorch cpu index)"
  rm -rf "wheelhouse/${arch}"
  "${PY}" -m pip download -r requirements.txt \
    --only-binary=:all: \
    --platform ${plat} \
    --python-version 3.12 --implementation cp \
    -d "wheelhouse/${arch}" \
    -i https://pypi.tuna.tsinghua.edu.cn/simple \
    --extra-index-url https://download.pytorch.org/whl/cpu
done

echo "[wheelhouse] regenerating requirements-lock.txt + wheelhouse.sha256"
"${PY}" "${repo_root}/deploy/scripts/gen-python-lock.py"

echo "[wheelhouse] done. Commit requirements-lock.txt wheelhouse.sha256; wheelhouse/ itself stays untracked."
