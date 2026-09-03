# P1-SUP1 交付记录：统一 CI 与生产 Python 依赖闭包（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`。依据审核报告 §8。

## 1. 修改文件清单

| 文件 | 变更 |
|---|---|
| `ai-orchestrator/requirements.txt` | **新增 `--extra-index-url`（pytorch cpu index）+ `torch==2.13.0+cpu`**——消除"PyPI linux 默认装 CUDA 巨型版"歧义 |
| `ai-orchestrator/requirements-lock.txt` | **重写**：127 行（不完整、无 hash、缺 crewai/torch）→ **173 包全量闭包 + 每包 `--hash=sha256:`（linux 多平台 hash 一行多值）** |
| `ai-orchestrator/wheelhouse.sha256` | **新增**：346 文件（amd64+arm64 wheel）SHA-256 manifest |
| `ai-orchestrator/tools.sha256` | **新增**：kubectl + k8sgpt SHA-256（Dockerfile 构建时校验） |
| `ai-orchestrator/models.sha256` | **新增**：hf.tar.gz（bge-small-zh）+ chroma.tar.gz（onnx）SHA-256 |
| `ai-orchestrator/Dockerfile` | **多阶段重构**：删除 sp.tar.gz/pybin.tar.gz"从运行容器导出"；deps 阶段 `sha256sum -c` + `pip install --no-index --find-links=wheelhouse/${TARGETARCH} --require-hashes`；runtime 精确 COPY 代码白名单（wheelhouse/bin 不入最终镜像层） |
| `ai-orchestrator/.gitignore` / `.dockerignore` | wheelhouse/ 入 ignore；.dockerignore 仅排除无关内容（专用 COPY 路径不得排除） |
| `deploy/scripts/build-wheelhouse.sh` | **新增**：可复现生成 wheelhouse（amd64/arm64，清华 + pytorch index） |
| `deploy/scripts/gen-python-lock.py` | **新增**：从 wheelhouse 生成 hash lock + sha256 manifest |
| `bin/` | 删除废弃 `sp.tar.gz`（403MB）与 `pybin.tar.gz`（旧导出方式） |
| `.github/workflows/aiops-workflow-gates.yml` | Python jobs 安装行加 pytorch `--extra-index-url`（lock 中 torch+cpu 仅存于该 index） |

## 2. 根因（报告 §8.1 实锤）

CI（3.14 + 无 torch/crewai 的 127 行 lock）与生产（bin/sp.tar.gz 导出：torch 2.13.0+cpu / chromadb 1.1.1 / sentence-transformers 5.7.0）依赖闭包**双轨分裂**；且**旧 lock 本身不可安装为一致闭包**——chromadb==1.5.9 违反 crewai 对 chromadb 的约束（crewai 1.15.x 实际解析出 1.1.1）。venv-312 freeze 亦被 dev 工具污染（tomli_w 1.2.0 违反 crewai `~=1.1.0`）。

## 3. 修复

- 以 `requirements.txt`（显式 torch+cpu）经 **pip resolver 干净解析**：一致闭包 173 包（crewai 1.15.18 / torch 2.13.0+cpu / chromadb 1.1.1）。
- 双平台 wheelhouse：amd64（494MB）+ arm64（444MB），经 `--platform manylinux* --python-version 3.12` 交叉下载；wheel 本体不入 Git（`wheelhouse/` 忽略），hash manifest + lock 入 Git。
- Dockerfile：deps 阶段离线校验安装（`--no-index --require-hashes`，无网络）；wheelhouse COPY 层仅存中间 stage，**不进入最终镜像/registry**；模型/工具均 `sha256sum -c` 校验后才使用。
- 8 个 Go Dockerfile golang 基础镜像统一 1.26.6（随 P1-SCA1 标准库漏洞修复）。

## 4. 验证（含报告 §8.8 关闭条件）

```bash
# 镜像构建（orbstack linux/arm64，与生产同平台）
docker build --target deps   # sha256sum -c 346 文件通过 + 173 包离线 --require-hashes 安装成功
docker build .               # 完整镜像 4.12GB（原 8.25GB）

# 容器内验证（§8.8）
python --version              # 3.12.14
pip freeze --all              # 174 行（=173 包 lock + pip）
python -c "import torch, chromadb, crewai, langgraph, sentence_transformers"
                              # imports ok, torch 2.13.0+cpu
AIOPS_ENV=production LLM_MOCK=true python -c "import main"
                              # [FATAL] fail-fast 正确（P2-R5 相关防护生效）
python -c "import main"       # ok, routes 96

# 新闭包测试（fresh 3.12 venv + lock + dev）
pytest -q                     # 1322 passed, 1 skipped
```

## 5. 架构契约 / 兼容性

- 无架构契约变更。生产 Python 3.12（3.12.14）与 CI workflow（已设 3.12）minor 版本对齐。
- `requirements-lock.txt` 的 hash 面向 **linux 生产 wheel**；macOS 本机开发不能 `--require-hashes` 安装（平台 wheel 不同，pip 自动强制 hash 校验失败）——需去 hash 或装 PyPI mac torch（无 +cpu，mac wheel 本身 CPU-only）。已记录于文档与 lock 文件头注释。
- kubectl/k8sgpt 二进制与模型制品：tools.sha256/models.sha256 入 Git，Dockerfile 构建时强制校验。
- 诚实边界：amd64 wheelhouse 由 CI/生产 linux amd64 构建验证；本机仅 arm64 实构。release 阶段（P1-REL1）将对最终 commit 重建 wheelhouse + 镜像 + 完整证据链。

## 6. 关闭结论：CLOSED

| §8 关闭条件 | 状态 |
|---|---|
| CI=production 同 Python minor | ✅ 3.12（CI workflow 已设） |
| 真实 production runtime 全量锁定 + hash | ✅ 173 包全 pin + hash（含 crewai/sentence-transformers/torch/chromadb/langgraph） |
| kubectl/k8sgpt/model 等 checksum | ✅ tools.sha256/models.sha256 + Dockerfile 校验 |
| Dockerfile 从"导出 site-packages"改为 lock 确定性构建 | ✅ 多阶段 deps 校验安装 |
| fresh 复现相同 dependency manifest | ✅ 容器内 freeze 174 = lock 173（平台 wheel 差异仅 +pip 自身）；§8.8 关键验证通过 |
| CI 与 production 用同 lock | ✅ workflow 安装行已更新 |
