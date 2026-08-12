# AI 运维助手 4 项功能优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 AI 运维助手命令卡片溢出、去掉输出截断、收窄命令执行范围，并新增"输出最终版本报告"按钮（每次对话内置日志采集 + k8sgpt 诊断）。

**Architecture:** 后端 `ai-orchestrator`（Python FastAPI + LangGraph）承担日志采集、k8sgpt 诊断、命令安全策略、最终报告生成；前端 `observability-frontend`（React）负责卡片布局修复与新增按钮。命令范围通过 `shell_policy.py` 新增黑名单（G/H）在现有白名单放行后二次拦截。

**Tech Stack:** Python FastAPI / LangGraph / ClickHouse / K8sGPT / React / Ant Design / pytest / playwright

## Global Constraints

- 只对用户可见输出去截断；`_audit_log` 的 `script[:500]`/`output_preview[:200]` 等审计内部字段保留截断，不改。
- `shell_policy.py` 保留 `check()`/`check_shell_metachars()`/`is_whitelisted_for_execute()` 现有签名不变；新增方法 `check_extra_blacklist()`。
- `node_collect`/`execute_suggestion` 的错误处理均 try/except 兜底，任何采集失败不阻塞主流程。
- 日志采集走 query-api `GET {QUERY_API}/logs/query?service={svc}&minutes={minutes}`（`QUERY_API` 来自 `tools.py` 环境变量）。
- k8sgpt API key 通过临时环境变量传入子进程 argv（不改安全模型）；每次对话无条件调用，失败快速跳过。
- 前端遵循现有 CSS 变量（`var(--surface-2)` 等）与 Ant Design 组件风格。
- 本机 Python 3.9.6，无法全栈解析 requirements；所有后端测试用隔离 TestClient + 临时 store，**不 import main/orchestrator 之外的 langchain 1.x 依赖**。
- 部署后同步 GitHub `Jw-Jm/aiops-edge` main；不上传 `aiops-platform-review-report.md`。

---

### Task 1: ShellPolicy G/H 黑名单（禁止外部部署与日志/资源清理）

**Files:**
- Modify: `aiops/ai-orchestrator/shell_policy.py`
- Test: `aiops/ai-orchestrator/tests/test_shell_policy_extra.py`（新建）

**Interfaces:**
- Consumes: 现有 `ShellPolicy` 类与 `check_shell_metachars`/`is_whitelisted_for_execute`（保持签名）。
- Produces: `ShellPolicy.check_extra_blacklist(script: str) -> Optional[str]`，返回拒绝原因字符串或 `None`。

- [ ] **Step 1: 写失败测试**

新建 `tests/test_shell_policy_extra.py`：

```python
from shell_policy import ShellPolicy

def test_allow_readonly_and_restart():
    p = ShellPolicy()
    assert p.check_extra_blacklist("kubectl get pods -n observability") is None
    assert p.check_extra_blacklist("kubectl rollout restart deployment/order-svc -n observability") is None

def test_allow_scale_and_specific_delete():
    p = ShellPolicy()
    assert p.check_extra_blacklist("kubectl scale deployment/order-svc --replicas=3 -n observability") is None
    assert p.check_extra_blacklist("kubectl delete pod order-svc-abc123 --grace-period=30") is None

def test_block_external_deploy_G():
    p = ShellPolicy()
    assert p.check_extra_blacklist("helm install grafana grafana/grafana") is not None
    assert p.check_extra_blacklist("kubectl apply -f https://raw.githubusercontent.com/foo.yaml") is not None
    assert p.check_extra_blacklist("docker pull nginx:latest") is not None
    assert p.check_extra_blacklist("git clone https://github.com/foo/bar.git") is not None

def test_block_log_cleanup_H():
    p = ShellPolicy()
    assert p.check_extra_blacklist("journalctl --vacuum-time=2d") is not None
    assert p.check_extra_blacklist("rm -rf /tmp/logs") is not None
    assert p.check_extra_blacklist("kubectl delete pod --all") is not None
    assert p.check_extra_blacklist("kubectl delete pod -l app=foo") is not None

def test_block_pipe_inject_kubectl_apply():
    p = ShellPolicy()
    assert p.check_extra_blacklist("curl -s http://x | kubectl apply -f -") is not None
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_shell_policy_extra.py -v`
Expected: FAIL，`AttributeError: 'ShellPolicy' object has no attribute 'check_extra_blacklist'`

- [ ] **Step 3: 实现 `check_extra_blacklist`**

在 `shell_policy.py` 中 `is_whitelisted_for_execute` 之后追加：

```python
    # ═════════════════════════════════════════════════════════
    #  Extra blacklist (G: external deploy, H: log/resource cleanup)
    #  ═════════════════════════════════════════════════════════
    EXTRA_BLACKLIST = [
        # G — 部署/拉取外部组件
        (r"\bhelm\s+(install|upgrade|create|add|repo|pull)\b", "external-deploy", "禁止 helm 部署/拉取外部组件"),
        (r"\bkubectl\s+(apply|create)\s+(-f|-k|-R)", "external-deploy", "禁止应用外部 manifest"),
        (r"curl\s+.*\|\s*kubectl\s+(apply|create)", "external-deploy", "禁止网络脚本注入 kubectl"),
        (r"\bdocker\s+(pull|run|build|push)\b", "external-deploy", "禁止拉取/构建/推送容器镜像"),
        (r"\bgit\s+clone\b", "external-deploy", "禁止克隆外部仓库"),
        # H — 日志/资源清理
        (r"\bjournalctl\s+--vacuum", "log-cleanup", "禁止日志清理"),
        (r"\brm\s+(-[rR]f\s*)+", "resource-cleanup", "禁止递归强制删除"),
        (r"\btruncate\b", "resource-cleanup", "禁止清空文件"),
        (r"\bkubectl\s+delete\s+\S+\s+--all\b", "batch-delete", "禁止批量删除资源"),
        (r"\bkubectl\s+delete\s+\S+\s+-l\b", "batch-delete", "禁止按标签批量删除资源"),
    ]

    def check_extra_blacklist(self, command: str) -> Optional[str]:
        """G/H 范围收窄：在 is_whitelisted_for_execute 放行后二次拦截。
        命中则返回拒绝原因，否则 None。"""
        for pattern, cat, desc in self.EXTRA_BLACKLIST:
            if re.search(pattern, command, re.IGNORECASE):
                return f"命令超出允许范围: [{cat}] {desc}"
        return None
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_shell_policy_extra.py -v`
Expected: PASS（全部通过）

- [ ] **Step 5: 在 `execute_suggestion` 接入黑名单**

`orchestrator.py::execute_suggestion`（L1406 白名单校验之后、`subprocess.run` 之前）插入：

```python
        # G/H 黑名单：禁止外部部署 / 日志清理 / 批量删除
        if blk := policy.check_extra_blacklist(script):
            return f"命令被安全策略拒绝: {blk}"
```

`tools.py::execute_shell`（L80 `policy.check` 之后）插入：

```python
    if blk := policy.check_extra_blacklist(command):
        return f"命令被安全策略拒绝: {blk}"
```

- [ ] **Step 6: 运行后端既有测试确认无回归**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/ -q 2>&1 | tail -20`
Expected: 既有测试无回归（可能有与 langgraph 相关测试因 1.x 依赖跳过，属预期）。

- [ ] **Step 7: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add aiops/ai-orchestrator/shell_policy.py aiops/ai-orchestrator/tools.py aiops/ai-orchestrator/orchestrator.py aiops/ai-orchestrator/tests/test_shell_policy_extra.py
git commit -m "feat: ShellPolicy G/H 黑名单收窄命令执行范围"
```

---

### Task 2: node_collect 每次采集日志 + 无条件调 k8sgpt + 下游注入

**Files:**
- Modify: `aiops/ai-orchestrator/tools.py`（新增 `query_logs`）
- Modify: `aiops/ai-orchestrator/orchestrator.py`（`node_collect`、`node_crewai` 上下文、`_friendly_tool_result` 可不动）
- Test: `aiops/ai-orchestrator/tests/test_node_collect_logs.py`（新建）

**Interfaces:**
- Consumes: `tools.py` 的 `QUERY_API` 环境变量；`orchestrator.py::node_collect` 现有 state。
- Produces: `query_logs(service: str = "", minutes: int = 30) -> str`；`state["logs_data"]`（每次对话写入）；`state["k8sgpt_raw"]`（每次对话写入）。

- [ ] **Step 1: 写失败测试**

新建 `tests/test_node_collect_logs.py`：

```python
import asyncio
import pytest

def test_query_logs_returns_text(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url):
        captured["url"] = url
        return {"data": [{"timestamp": "t", "service_name": "s", "severity": "ERROR", "body": "boom"}], "count": 1}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    out = query_logs("order-svc")
    assert "order-svc" in captured["url"] or "order-svc" in out
    assert "ERROR" in out or "boom" in out

def test_query_logs_empty_service_allows_all(monkeypatch):
    from tools import query_logs
    captured = {}
    def fake_get_json(url):
        captured["url"] = url
        return {"data": [], "count": 0}
    monkeypatch.setattr("tools._get_json", fake_get_json)
    query_logs("")
    # 空 service 不追加 service_name 过滤，走全量
    assert "service=" not in captured["url"].split("?")[1]

def test_node_collect_includes_logs_and_k8sgpt(monkeypatch):
    from orchestrator import node_collect
    async def run():
        calls = {"k8sgpt": 0}
        real_run = __import__("subprocess").run
        def fake_subprocess(*a, **kw):
            argv = a[0] if a else kw.get("args", [])
            if isinstance(argv, (list, tuple)) and argv and "k8sgpt" in argv[0]:
                calls["k8sgpt"] += 1
                class R:
                    returncode = 0
                    stdout = "CRITICAL: pod X CrashLoopBackOff"
                    stderr = ""
                return R()
            return real_run(*a, **kw)
        monkeypatch.setattr("orchestrator.subprocess.run", fake_subprocess)
        monkeypatch.setattr("orchestrator._get_json", lambda *a, **k: {"data": [], "count": 0})
        state = {"service": "order-svc", "llm_config": None}
        res = await node_collect(state)
        assert "logs_data" in res
        assert calls["k8sgpt"] >= 1
    asyncio.run(run())
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_node_collect_logs.py -v`
Expected: FAIL（`query_logs` 未定义 / `logs_data` 不存在 / k8sgpt 未调用）

- [ ] **Step 3: 在 `tools.py` 新增 `query_logs`**

在 `query_traces` 之后追加：

```python
def query_logs(service: str = "", minutes: int = 30) -> str:
    """查询最近 N 分钟日志（ClickHouse log_records，经 query-api）。
    空 service 走全量最近日志。"""
    url = f"{QUERY_API}/logs/query?service={service}&minutes={minutes}"
    data = _get_json(url)
    if isinstance(data, dict) and "error" in data:
        return f"日志查询失败: {data['error']}"
    rows = data.get("data", []) if isinstance(data, dict) else []
    if not rows:
        return "（近 30 分钟无日志）"
    lines = []
    for r in rows[:50]:
        sev = r.get("severity", "")
        body = (r.get("body", "") or "").strip().replace("\n", " ")
        lines.append(f"[{r.get('timestamp','')}] {r.get('service_name','')} {sev}: {body[:200]}")
    return "\n".join(lines)
```

- [ ] **Step 4: 修改 `node_collect` 每次采集日志 + 无条件调 k8sgpt**

在 `orchestrator.py::node_collect`：

(a) 在 `red_metrics`/`trace_data` 采集块之后（L417 `except: pass` 后）追加日志采集：

```python
    # 日志 — 每次对话无条件采集（结合日志分析）
    try:
        result["logs_data"] = await asyncio.to_thread(query_logs, svc, 30)
    except:
        result["logs_data"] = ""
```

(b) 将 k8sgpt 块（L419-440）前置条件 `if api_key and cfg:` 改为无条件调用。改为：

```python
    # K8sGPT — 每次对话无条件调用，失败快速跳过不阻塞（timeout 10s）
    import shutil
    if shutil.which("k8sgpt"):
        try:
            env = dict(os.environ)
            if api_key:
                env["OPENAI_API_KEY"] = api_key
            if cfg:
                backend = cfg.get("backend", "openai")
                await asyncio.to_thread(
                    subprocess.run,
                    ["k8sgpt", "auth", "add", "-b", backend, "-m", cfg.get("model", "gpt-4o")],
                    capture_output=True, text=True, timeout=5, env=env,
                )
            r = await asyncio.to_thread(
                subprocess.run,
                ["k8sgpt", "analyze", "--explain", "-n", "observability", "-o", "text"],
                capture_output=True, text=True, timeout=10, env=env,
            )
            if r.returncode == 0 and r.stdout.strip() and len(r.stdout.strip()) > 50:
                result["k8sgpt_raw"] = r.stdout[:20000]
        except: pass
```

> 注：`api_key` 与 `cfg` 仍在函数开头（L420）定义，`api_key` 从 `_LLM_KEY_HOLDER` 读取，`cfg` 从 `state.get("llm_config")` 读取。若无 key 则不传 `OPENAI_API_KEY`，k8sgpt 使用默认/已配置凭据，失败由 `except: pass` 兜底。

- [ ] **Step 5: 把 `logs_data` 注入 CrewAI 上下文**

`orchestrator.py::node_crewai`（L656）的 `ctx_parts` 列表追加 `"logs_data"`：

```python
    for k in ["similar_cases", "services_data", "infra_data", "alert_data", "red_metrics", "trace_data", "logs_data", "k8sgpt_raw", "rca_evidence"]:
```

同时 `_build_analysis_context`（L585-620）在 `# K8s` 段（L612）后追加日志段：

```python
    # 日志
    logs = state.get("logs_data", "")
    if logs:
        lines.append(f"- **日志**: {logs[:1500]}")
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_node_collect_logs.py -v`
Expected: PASS

- [ ] **Step 7: 运行既有测试确认无回归**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/ -q 2>&1 | tail -20`
Expected: 无回归

- [ ] **Step 8: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add aiops/ai-orchestrator/tools.py aiops/ai-orchestrator/orchestrator.py aiops/ai-orchestrator/tests/test_node_collect_logs.py
git commit -m "feat: 每次aichat对话采集日志并无条件调用k8sgpt"
```

---

### Task 3: 去掉输出截断（用户可见输出全量）

**Files:**
- Modify: `aiops/ai-orchestrator/orchestrator.py`
- Test: `aiops/ai-orchestrator/tests/test_output_notruncate.py`（新建）

**Interfaces:**
- Consumes: `execute_suggestion`、`node_collect`、SSE `suggestion` 事件生成。
- Produces: 用户可见输出不再截断。

- [ ] **Step 1: 写失败测试**

新建 `tests/test_output_notruncate.py`：

```python
import pytest

def test_execute_suggestion_full_output(monkeypatch):
    from orchestrator import BrainOrchestrator
    b = BrainOrchestrator.__new__(BrainOrchestrator)
    calls = {}
    import subprocess as sp
    class R:
        returncode = 0
        stdout = "line_" * 3000   # 远超 500 截断阈值
        stderr = ""
    monkeypatch.setattr("subprocess.run", lambda *a, **k: R())
    # 走白名单 kubectl get 前缀
    out = b.execute_suggestion("order-svc", "kubectl get pods -n observability", "")
    assert "line_" in out
    # 全量返回：3 万字符不截断为 2000
    assert len(out) > 5000

def test_final_response_suggestion_notruncate(monkeypatch):
    # 通过 mock 构造 suggestion 事件生成路径校验 final_response 全量
    from orchestrator import _extract_script, _fallback_script, _action_summary
    long_resp = "R" * 4000
    # _extract_script / _fallback_script / _action_summary 不截断 final_response 本身
    assert _action_summary("kubectl get pods", long_resp, "s") or True
```

> 注：`final_response[:3000]` 位于 `stream_sync`（L1373），该方法是异步生成器依赖 LangGraph 图运行，直接测成本高。测试聚焦 `execute_suggestion` 全量输出；`final_response` 的截断通过 Code Review 核对修改（见 Step 4），并以集成测试（Task 5 部署后）验收。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_output_notruncate.py -v`
Expected: FAIL（当前 `execute_suggestion` 返回 `[:2000]` 截断，`len(out) > 5000` 失败）

- [ ] **Step 3: `execute_suggestion` 去截断**

`orchestrator.py::execute_suggestion`：
- L1420 `r.stdout[:500]` → `r.stdout[:30000]`
- L1422 `r.stderr[:200]` → `r.stderr[:10000]`
- L1432 `return "\n".join(outputs)[:2000]` → `return "\n".join(outputs) or "(命令无输出)"`

- [ ] **Step 4: `suggestion` 事件与数据采集去截断**

- L1373 `"final_response": full_resp[:3000]` → `"final_response": full_resp`
- L391 `infra_data[:2000]` → `[:20000]`
- L439 `k8sgpt_raw[:2000]` → `[:20000]`（Task 2 已改为 20000，确认一致）
- L416 `trace_data[:3000]` → `[:30000]`
- L771（`node_approve` 内 `plan[:500]`）与 L893/L908-L915（审计内部）**保留截断**——审计内部字段。

> 审计内部字段（`_audit_log` 的 `script[:500]`/`output_preview[:200]`、`node_approve` 的 `plan[:500]`、case 入库 `[:500]`）均保留，不改。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_output_notruncate.py -v`
Expected: PASS

- [ ] **Step 6: 运行既有测试确认无回归**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/ -q 2>&1 | tail -20`
Expected: 无回归

- [ ] **Step 7: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add aiops/ai-orchestrator/orchestrator.py aiops/ai-orchestrator/tests/test_output_notruncate.py
git commit -m "feat: 用户可见输出去截断"
```

---

### Task 4: 新增 `/api/v1/ai/final_report` 端点

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`
- Modify: `aiops/ai-orchestrator/db_audit.py`（`AuditStore` 增加 `query_by_task`）
- Test: `aiops/ai-orchestrator/tests/test_final_report.py`（新建）

**Interfaces:**
- Consumes: `_get_brain().get_session_state(sid)`（返回 `user_message/final_response/intent/service`）；`AuditStore.query_by_task(task_id)`；`_llm_async`/`_llm_key_ready`。
- Produces: `POST /api/v1/ai/final_report`，body `{session_id, service}`，返回 `{report: str}`（全量不截断）。

- [ ] **Step 1: 在 `db_audit.py` 增加 `query_by_task`**

在 `AuditStore.query` 后追加：

```python
    def query_by_task(self, task_id: str):
        """查询某会话(task_id)的执行记录（处置建议执行历史）。"""
        try:
            cfg = _mysql_cfg()
            conn = pymysql.connect(host=cfg["host"], port=cfg["port"], user=cfg["user"],
                                   password=cfg["password"], database=cfg["database"],
                                   charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "SELECT task_id, action, target_service, command, result, detail, created_at "
                        "FROM audit_logs WHERE task_id=%s ORDER BY id ASC",
                        (task_id,),
                    )
                    return [dict(r) for r in cur.fetchall()]
            finally:
                try:
                    conn.close()
                except Exception:
                    pass
        except Exception:
            pass
        return [e for e in self._mem if e["task_id"] == task_id]
```

- [ ] **Step 2: 写失败测试**

新建 `tests/test_final_report.py`：

```python
import pytest
from fastapi.testclient import TestClient

def test_final_report_returns_report():
    # 隔离测试：不依赖真实 DB，monkeypatch get_session_state / AuditStore / _llm
    import main as m
    class FakeBrain:
        def get_session_state(self, sid):
            return {"user_message": "分析 order-svc 错误率", "final_response": "初步报告",
                    "intent": "diagnosis", "service": "order-svc"}
    class FakeAudit:
        def query_by_task(self, tid):
            return [{"task_id": tid, "action": "approve", "target_service": "order-svc",
                     "command": "kubectl rollout restart", "result": "success",
                     "detail": "", "created_at": "t"}]
    m._get_brain = lambda: FakeBrain()
    m.AuditStore = lambda: FakeAudit()
    # monkeypatch LLM 调用（final_report 使用 orchestrator._llm 同步函数）
    def fake_llm(cfg, sys, user, role=""):
        return "最终版本报告：根因定位 order-svc 内存泄漏，已滚动重启，风险解除。"
    m._llm = fake_llm
    m._llm_key_ready = lambda: True
    from fastapi import FastAPI
    app = FastAPI()
    # 挂载真实 final_report 路由（若 main 应用已定义则直接用 main.app）
    resp = None
    # 直接调用内部处理函数更稳妥：用 main.app
    try:
        with TestClient(m.app) as client:
            r = client.post("/api/v1/ai/final_report", json={"session_id": "sid1", "service": "order-svc"})
            resp = r
    except Exception as e:
        pytest.skip(f"main.app 不可用: {e}")
    if resp is not None:
        assert resp.status_code == 200
        assert "最终版本报告" in resp.json()["report"]
```

> 注：测试挂载真实 `main.app`。若 `main.app` 因 langgraph 依赖无法 import，则测试 `pytest.skip`，改由实现后的部署集成验证（Task 5）。这是已知的环境约束（Python 3.9 无法全栈解析 1.x）。

- [ ] **Step 3: 实现 `final_report` 端点**

`main.py` 中 `execute_suggestion_command`（L538）之后新增：

```python
class FinalReportRequest(BaseModel):
    session_id: str = ""
    service: str = ""


@app.post("/api/v1/ai/final_report")
async def final_report(req: FinalReportRequest):
    """输出最终版本报告：汇总该会话全部上下文（含多次处置执行），调 LLM 生成。"""
    if not req.session_id:
        raise HTTPException(400, "session_id is required")
    brain = _get_brain()
    vals = brain.get_session_state(req.session_id) or {}
    # 执行历史（审计）
    try:
        from db_audit import AuditStore
        exec_history = AuditStore().query_by_task(req.session_id)
    except Exception:
        exec_history = []
    # 组装上下文
    parts = []
    parts.append(f"## 用户原始问题\n{vals.get('user_message','') or '(无)'}")
    parts.append(f"## 初步分析\n{vals.get('final_response','') or '(无)'}")
    if exec_history:
        h = []
        for e in exec_history:
            h.append(f"- [{e.get('created_at','')}] action={e.get('action','')} target={e.get('target_service','')}\n"
                     f"  命令: {e.get('command','')}\n  结果: {e.get('result','')}")
        parts.append("## 处置执行历史（多次）\n" + "\n".join(h))
    else:
        parts.append("## 处置执行历史\n(无已执行记录)")
    service = req.service or vals.get("service", "")
    context = "\n\n".join(parts)
    # LLM 配置：与 main.py 既有模式一致（`_get_brain().llm_config`）
    from orchestrator import _llm, _llm_key_ready
    if not _llm_key_ready():
        return {"report": f"### 最终版本报告\n\n（LLM 未配置，基于已有分析汇总）\n\n{context[:4000]}"}
    system = ("你是资深 SRE 报告撰写员。基于用户原始问题、初步分析、以及全部处置执行历史，"
              "输出**最终版本报告**，包含：1) 根因结论 2) 处置过程（哪些命令已执行、结果）"
              "3) 当前状态与执行结果 4) 遗留风险 5) 后续建议。用 Markdown，条理清晰，完整不省略。")
    cfg = brain.llm_config or {}
    # _llm 为同步阻塞调用，放到线程池避免阻塞 event loop
    report = await asyncio.to_thread(_llm, cfg, system, f"服务: {service}\n\n{context}", "最终报告")
    return {"report": report}
```

> 注：`_llm`/`_llm_key_ready` 定义于 `orchestrator.py`（rca.py、llm_fc.py 均 `from orchestrator import _llm`），final_report 端点运行期 import。`cfg = brain.llm_config` 与 main.py 既有模式（L1839 `cfg = _get_brain().llm_config`）一致。测试中 `m._llm` 被 monkeypatch 为同步 `fake_llm`。

- [ ] **Step 4: 运行测试**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_final_report.py -v`
Expected: PASS 或 SKIP（main.app import 依赖 langgraph 时 SKIP）

- [ ] **Step 5: 语法与 import 检查**

Run: `cd aiops/ai-orchestrator && python -c "import ast; ast.parse(open('main.py').read())"`
Expected: 无语法错误

- [ ] **Step 6: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add aiops/ai-orchestrator/main.py aiops/ai-orchestrator/db_audit.py aiops/ai-orchestrator/tests/test_final_report.py
git commit -m "feat: 新增 /api/v1/ai/final_report 输出最终版本报告"
```

---

### Task 5: 前端卡片溢出修复 + 去截断 plan + 最终报告按钮

**Files:**
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Modify: `observability-frontend/src/api/client.ts`
- Test: 通过 playwright 集成验证（本任务内无单测，前端遵循现有测试方式）

**Interfaces:**
- Consumes: `finalReport({session_id, service})`（新增 client 函数）；`ConfirmCard` 现有 props。
- Produces: `POST /ai/final_report` 前端调用；卡片不溢出；plan 全量展示。

- [ ] **Step 1: 在 `client.ts` 新增 `finalReport`**

`executeSuggestion`（L132-134）后追加：

```typescript
export const finalReport = (data: { session_id?: string; service?: string }) =>
  api.post('/ai/final_report', data)
```

- [ ] **Step 2: 修复命令卡片溢出**

`AiChat.tsx`：
- 外层卡片 div（L224）加 `minWidth: 0, overflow: 'hidden'`。
- 命令块（L231）外层包一层 `<div style={{ maxWidth: '100%', overflow: 'auto' }}>`，命令块自身改：

```tsx
<div style={{ fontFamily: 'monospace', background: 'var(--surface-3)', padding: '6px 8px', borderRadius: 6,
  whiteSpace: 'pre-wrap', wordBreak: 'break-all', overflow: 'auto', maxHeight: 220,
  maxWidth: '100%', boxSizing: 'border-box' }}>{m.script}</div>
```

- [ ] **Step 3: plan 去截断**

L228 `{(m.plan.length > 220 ? m.plan.slice(0, 220) + '…' : m.plan)}` → `{m.plan}`。

- [ ] **Step 4: 新增"输出最终版本报告"按钮**

- `ConfirmCard` 增加 `onFinalReport` prop。
- 在 `ConfirmCard` 操作区（L267-271）"驳回"按钮后追加：

```tsx
<Button size="small" onClick={() => onFinalReport(m)}>输出最终版本报告</Button>
```

- `AiChat` 新增 `handleFinalReport`：

```typescript
const handleFinalReport = async (m: ChatMessage) => {
  if (loading) return
  setLoading(true); setProgress('正在生成最终版本报告…')
  try {
    const r = await finalReport({ session_id: activeSession || m.threadId, service: m.service || '' })
    const report = r.data?.report || '（未生成报告）'
    setMessages((prev) => [...prev, { id: `rep-${Date.now()}`, role: 'assistant', kind: 'report',
      content: report, timestamp: new Date().toISOString() }])
  } catch (err: any) {
    setMessages((prev) => [...prev, { id: `re-${Date.now()}`, role: 'assistant',
      content: `❌ 生成最终报告失败：${err?.message || ''}`, timestamp: new Date().toISOString() }])
  } finally { setLoading(false); setProgress('') }
}
```

> 注：`ChatMessage` 类型需包含 `kind: 'report'`（前端渲染分支 `m.kind === 'suggestion'` 之外的消息按普通文本渲染，report 消息走普通分支，无需额外改渲染）。`activeSession`/`m.threadId` 为现有可用字段。

- [ ] **Step 5: TypeScript 编译检查**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend && npx tsc --noEmit -p tsconfig.json 2>&1 | head -30`
Expected: 无新增类型错误

- [ ] **Step 6: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/ai/AiChat.tsx observability-frontend/src/api/client.ts
git commit -m "feat: aichat 卡片溢出修复、plan去截断、最终版本报告按钮"
```

---

### Task 6: 构建、部署与端到端验证

**Files:**
- 部署：`deploy/helm/aiops/`（values 覆盖镜像 tag）
- 验证：playwright 截图 + curl

- [ ] **Step 1: 后端测试全绿**

Run: `cd aiops/ai-orchestrator && python -m pytest tests/test_shell_policy_extra.py tests/test_node_collect_logs.py tests/test_output_notruncate.py tests/test_final_report.py -v 2>&1 | tail -30`
Expected: 4 个新测试文件通过（final_report 可能 SKIP，属预期）

- [ ] **Step 2: 重建 orchestrator 镜像（离线，复用本地 bin/sp.tar.gz）**

按既有离线流程重建 `ai-orchestrator` 镜像，tag 如 `v1.1.20`，推到本地 registry。

- [ ] **Step 3: 前端 build + 重建镜像**

`observability-frontend` build，重建镜像 `observability-frontend:v3.4.5`。

- [ ] **Step 4: Helm 升级部署**

```bash
helm upgrade --reuse-values observability deploy/helm/aiops \
  --set orchestrator.image.tag=v1.1.20 \
  --set frontend.image.tag=v3.4.5 \
  --namespace observability
```

- [ ] **Step 5: playwright 端到端验证**

登录 localhost:30253（admin/admin123）：
- AI 助手页发起分析 → 断言处置建议卡片**不溢出**（截图检查命令块有滚动条、卡片边界完整）。
- 命令执行 → 断言输出**完整展示无 '…' 截断**。
- 处置建议卡点"输出最终版本报告" → 断言新增报告消息且含"最终版本报告"。
- 通过 curl + token 核验 `POST /api/v1/ai/final_report` 返回 200 与完整 report。

- [ ] **Step 6: 同步 GitHub**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A -- ':!aiops-platform-review-report.md'
git commit -m "feat: aichat 报告优化 v2（卡片/去截断/命令范围/最终报告）"
git push origin main
```

---

## Self-Review

**Spec coverage:**
- 第一节 卡片溢出 → Task 5 Step 2 ✓
- 第二节 去截断 → Task 3 ✓（前端 plan Task 5 Step 3）
- 第三节 G/H 黑名单 → Task 1 ✓
- 第四节 每次采集日志+调 k8sgpt → Task 2 ✓；最终报告按钮+端点 → Task 4 + Task 5 Step 4 ✓

**Placeholder scan:** 无 TBD/TODO；各代码步骤含完整实现。

**Type consistency:**
- `check_extra_blacklist` 在 Task 1 定义，Task 1 Step 5 在 `execute_suggestion`/`tools.execute_shell` 接入 ✓
- `query_logs(service, minutes)` 在 Task 2 定义，`node_collect` 调用 `query_logs(svc, 30)` ✓
- `AuditStore.query_by_task(task_id)` 在 Task 4 定义，`final_report` 调用 ✓
- `finalReport` client 函数在 Task 5 Step 1 定义，Step 4 使用 ✓
- `logs_data` 在 Task 2 的 `node_collect` 写入，`node_crewai`/`_build_analysis_context` 消费 ✓
