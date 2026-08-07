# 依赖完全修复：LangGraph 1.x 升级 + 本地 Python 3.12

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 `import orchestrator` 失败问题：升级 langgraph 到 1.x，升级本地 Python 到 3.12，确保本地和 Docker 两端均可正常 import 和运行

**Architecture:** 仅限依赖层修改——两行 `requirements.txt` 版本约束变更，可能的 `orchestrator.py` 导入路径适配（若 langgraph 1.x 中 SqliteSaver 需要从独立包导入），无新代码、无功能变更

**Tech Stack:** Python 3.12, langgraph 1.x, langgraph-checkpoint-sqlite 1.x, crewai 1.x, FastAPI

## Global Constraints

- Python >= 3.12（与 Dockerfile `python:3.12-slim` 对齐）
- `langgraph>=1.0,<2.0`
- `langgraph-checkpoint-sqlite>=1.0,<2.0`（主版本与 langgraph 对齐）
- FlowEditor 42 测试必须继续通过
- `import orchestrator` 和 `import main` 必须成功
- uvicorn 启动 8080 端口 `/health` 返回 200

---

### Task 1: 安装 Python 3.12

**Files:**
- 无文件修改（系统级安装）

- [ ] **Step 1: 安装 Python 3.12**

使用 `install_binary` 工具安装。此工具为 IDE 内置，会自动检测并安装指定版本。

调用：`install_binary(type="python", version="3.12")`

- [ ] **Step 2: 确认安装成功**

```bash
python3.12 --version
```

预期输出: `Python 3.12.x`

---

### Task 2: 修改 requirements.txt + 首次 pip install

**Files:**
- Modify: `ai-orchestrator/requirements.txt:2-3`

- [ ] **Step 1: 修改 requirements.txt 的 langgraph 族系**

将第 2-3 行：
```
langgraph>=0.2.23,<0.3
langgraph-checkpoint-sqlite>=2.0
```
替换为：
```
langgraph>=1.0,<2.0
langgraph-checkpoint-sqlite>=1.0,<2.0
```

- [ ] **Step 2: 创建 Python 3.12 virtualenv**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
python3.12 -m venv .venv-312
source .venv-312/bin/activate
```

- [ ] **Step 3: pip install 依赖**

```bash
pip install --upgrade pip
pip install -r requirements.txt
```

- [ ] **Step 4: 记录实际安装的版本号**

```bash
pip freeze | grep -E "langgraph|langchain|crewai" | sort
```

预期：`langgraph==1.2.x`、`langgraph-checkpoint-sqlite==1.0.x`、`crewai==1.15.x`，无 langchain 相关包（crewai 不需要）

- [ ] **Step 5: Commit**

```bash
git add requirements.txt
git commit -m "fix: upgrade langgraph to 1.x, align checkpoint-sqlite version"
```

---

### Task 3: import orchestrator 验证 + 可能的导入路径适配

**Files:**
- May modify: `ai-orchestrator/orchestrator.py:10`（仅当 import 失败时）

- [ ] **Step 1: 尝试 import orchestrator**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
source .venv-312/bin/activate
python -c "import orchestrator; print('orchestrator OK')"
```

- [ ] **Step 2: 判断结果，分支处理**

**如果成功** → 跳到 Step 4（无需修改 orchestrator.py）。

**如果失败且报错包含 `SqliteSaver`** → 继续 Step 3a。

**如果失败且报错包含其他 langgraph API** → 继续 Step 3b。

- [ ] **Step 3a: SqliteSaver 导入路径修复**

将 `orchestrator.py` 第 10 行：
```python
from langgraph.checkpoint.sqlite import SqliteSaver
```
改为：
```python
from langgraph_checkpoint_sqlite import SqliteSaver
```

然后重新运行 Step 1 验证。

- [ ] **Step 3b: 其他 langgraph API 适配**

根据实际报错中的符号名，使用 `python -c "from langgraph.xxx import YYY"` 逐个探测正确的导入路径，修正 `orchestrator.py` 第 9-11 行对应的 import 语句。每次修正后重新运行 Step 1。

- [ ] **Step 4: 如有 orchestrator.py 修改则提交**

```bash
git add orchestrator.py
git commit -m "fix: adapt langgraph import paths for 1.x"
```

---

### Task 4: import main 验证 + FlowEditor 回归测试

**Files:**
- 无修改

- [ ] **Step 1: import main**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
source .venv-312/bin/activate
python -c "import main; print('main OK')"
```

预期：成功打印 `main OK`（main.py 延迟加载 orchestrator，import 阶段不会触发 langgraph 初始化）

- [ ] **Step 2: 运行 FlowEditor 回归测试**

```bash
python -m pytest flow_engine/tests/ -v
```

预期：全部 42 个测试通过

- [ ] **Step 3: 如测试全部通过则提交**

```bash
git commit --allow-empty -m "test: verify import main + FlowEditor regression after langgraph upgrade"
```

---

### Task 5: uvicorn 冒烟测试

**Files:**
- 无修改

- [ ] **Step 1: 启动服务**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
source .venv-312/bin/activate
uvicorn main:app --host 0.0.0.0 --port 8080 &
sleep 3
```

- [ ] **Step 2: 检查健康端点**

```bash
curl -s http://localhost:8080/health | python -m json.tool
```

预期：返回 JSON，包含 `"status": "ok"` 或类似字段，HTTP 200

- [ ] **Step 3: 检查 FlowEditor API**

```bash
curl -s http://localhost:8080/api/flow/node-types | python -m json.tool
```

预期：返回 15 种节点类型的 JSON 数组

- [ ] **Step 4: 停止服务**

```bash
kill $(lsof -ti:8080) 2>/dev/null || true
```

---

### Task 6: 前端回归验证

**Files:**
- 无修改

- [ ] **Step 1: TypeScript 类型检查**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend
npx tsc --noEmit
```

预期：无类型错误

- [ ] **Step 2: Vite build**

```bash
npm run build
```

预期：构建成功，输出到 `dist/` 目录

- [ ] **Step 3: 提交验证结果**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git commit --allow-empty -m "verify: frontend tsc + build pass after dependency upgrade"
```

---

### Task 7: Docker build 验证（可选，耗时较长）

**Files:**
- 无修改

> 注：Docker build 可能需要拉取镜像层 + 下载依赖，耗时 3-10 分钟。若本地 Docker 不可用可跳过。

- [ ] **Step 1: Docker build**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
docker build --no-cache -t ai-orchestrator:fix .
```

- [ ] **Step 2: Docker 容器 import 验证**

```bash
docker run --rm ai-orchestrator:fix python -c "
import orchestrator; import main; print('Docker import OK')
"
```

预期：输出 `Docker import OK`

- [ ] **Step 3: 提交验证结果**

```bash
git commit --allow-empty -m "verify: docker build + import pass"
```
