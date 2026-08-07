# 依赖完全修复：LangGraph 升级到 1.x + 本地 Python 升级

**日期**: 2026-08-07
**范围**: ai-orchestrator 依赖版本修复（本地 + 部署镜像）
**驱动**: Task 11 "依赖修复 + 完整部署" — `import orchestrator` 失败（langgraph/langchain-core 冲突 + langgraph-checkpoint-sqlite 主版本不匹配）

---

## 1. 问题描述

### 1.1 直接症状

```python
import orchestrator
# ImportError: cannot import name 'RemoveMessage' from 'langchain_core.messages'
```

### 1.2 根因

| 项 | 现状 | 问题 |
|---|---|---|
| `requirements.txt` langgraph pin | `>=0.2.23,<0.3` | langgraph 0.2.x 的 `SqliteSaver` 在 core 包内，但 `langgraph-checkpoint-sqlite>=2.0` 是为 langgraph 2.x 设计的独立包，主版本冲突 |
| `requirements.txt` checkpoint pin | `>=2.0` | 与 langgraph `<0.3` 不兼容 |
| 本地 Python | 3.9.6 | `langgraph 1.x` 和 `crewai>=1.0` 要求 Python >= 3.10 |
| Docker 镜像 | `python:3.12-slim` | 镜像侧 Python 已正确，只需修正依赖版本 |

### 1.3 关键澄清

- **crewai 1.15.12 完全不依赖 langchain**（PyPI 明确声明 "completely independent of LangChain"），之前担心的 crewai/langchain 冲突是误报
- 项目唯一直接 `import langgraph` 的文件是 `orchestrator.py`（第 9-11 行），其他模块通过延迟加载间接使用

---

## 2. 设计

### 2.1 总体方案：升级到 langgraph 1.x

langgraph 1.0 于 2025 年 10 月发布，当前稳定版 1.2.10。API 向后兼容，导入路径不变。

### 2.2 requirements.txt 修改

```diff
# langgraph 族系
- langgraph>=0.2.23,<0.3
- langgraph-checkpoint-sqlite>=2.0
+ langgraph>=1.0,<2.0
+ langgraph-checkpoint-sqlite>=1.0,<2.0

# 其余 12 个依赖不变
crewai>=1.0
fastapi>=0.115
uvicorn[standard]>=0.32
sse-starlette>=2.0
chromadb>=0.4.24
sentence-transformers>=3.0
redis>=5.0
minio>=7.2
arq>=0.26
httpx>=0.28
pydantic>=2.0
prometheus-client>=0.20
```

两行改动，其余不变。

### 2.3 orchestrator.py 兼容性

langgraph 1.x 导入路径与 0.2.x 向后兼容：

```python
from langgraph.graph import StateGraph, END      # 不变
from langgraph.checkpoint.sqlite import SqliteSaver  # 不变
from langgraph.types import interrupt, Command    # 不变
```

若 1.x 中 `SqliteSaver` 已从 core 移入独立包 `langgraph-checkpoint-sqlite`，导入路径可能需改为：

```python
from langgraph.checkpoint.sqlite import SqliteSaver  # 兼容 shim（若保留）
# 或
from langgraph_checkpoint_sqlite import SqliteSaver   # 直接导入独立包
```

**策略**：优先不改代码，若 `pip install` 后 import 失败，再按实际报错修正。

### 2.4 本地 Python 升级

使用 `install_binary` 工具安装 Python 3.12，与 Dockerfile 的 `python:3.12-slim` 对齐：

```bash
install_binary python 3.12
```

### 2.5 Docker 镜像

Dockerfile 无需修改——基础镜像已是 `python:3.12-slim`，`pip install -r requirements.txt` 会自动解析新版本约束。

---

## 3. 验证策略

### 3.1 本地验证（Python 3.12）

```bash
# 1. 全新 virtualenv
python3.12 -m venv .venv-312 && source .venv-312/bin/activate

# 2. 安装依赖（使用修复后的 requirements.txt）
pip install -r requirements.txt

# 3. 导入验证
python -c "import orchestrator; print('orchestrator OK')"
python -c "import main; print('main OK')"

# 4. FlowEditor 回归测试（确保升级不破坏已有功能）
python -m pytest flow_engine/tests/ -v

# 5. 启动冒烟
uvicorn main:app --host 0.0.0.0 --port 8080 &
curl http://localhost:8080/health
curl http://localhost:8080/api/flow/node-types
```

### 3.2 Docker build 验证

```bash
cd ai-orchestrator
docker build --no-cache -t ai-orchestrator:fix .
docker run --rm ai-orchestrator:fix python -c "
import orchestrator; import main; print('Docker import OK')
"
```

---

## 4. 风险矩阵

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| `orchestrator.py` API 微调（`SqliteSaver` 导入路径） | 中 | 低 — 1 行修改 | 先试不改代码 import，失败再按报错修正 |
| `checkpoint` 独立包导入路径变化 | 中 | 低 — 1-2 行修改 | langgraph-checkpoint-sqlite 文档明确 |
| `crewai` 间接依赖冲突 | 极低 | — | 已确认 crewai 无 langchain 依赖 |
| Docker build 因镜像源/网络失败 | 极低 | — | Dockerfile 已有国内镜像回退 |
| FlowEditor 回归破坏 | 极低 | — | FlowEditor 不依赖 langgraph/orchestrator |

---

## 5. 自审清单

- [x] 无 TBD/TODO 占位符
- [x] 架构与功能描述一致：两行 requirements.txt + 可能的 orchestrator.py 小修
- [x] 范围聚焦单一：依赖升级，不涉及功能变更
- [x] 无歧义：验证步骤明确、预期结果可判断
