# AI 运维助手 4 项功能优化设计

日期：2026-08-12
状态：已获用户批准

## 背景

AI 运维助手（aichat）在使用中发现 4 个问题，需一次性修复并增强：

1. **命令仍超出卡片**：处置建议卡中命令块在超长单行命令时溢出卡片边界。
2. **显示信息被截断**：命令执行输出、最终报告、plan 展示均被截断。
3. **存在拉取/部署外部组件的处置建议**：需收窄命令执行白名单范围，把"部署外部组件"和"日志清理"类命令禁止。
4. **缺少最终版本报告能力**：新增"输出最终版本报告"按钮；且日志分析与 k8sgpt 诊断应内置于**每一次** aichat 对话（而非仅最终报告时）。

## 涉及仓库与组件

| 组件 | 路径 | 改动 |
|------|------|------|
| 后端编排 | `ai-orchestrator/orchestrator.py` | `node_collect`、`execute_suggestion`、`_friendly_tool_result`、下游 prompt |
| 后端路由 | `ai-orchestrator/main.py` | 新增 `POST /api/v1/ai/final_report` |
| 后端安全 | `ai-orchestrator/shell_policy.py` | 新增 G/H 黑名单 |
| 后端工具 | `ai-orchestrator/tools.py` | 新增 `query_logs` |
| 前端 | `observability-frontend/src/pages/ai/AiChat.tsx` | 卡片溢出修复、去截断、最终报告按钮 |
| 前端 | `observability-frontend/src/api/client.ts` | 新增 `finalReport` API |

---

## 第一节：命令卡片溢出修复（前端）

**根因**：`AiChat.tsx` 处置建议卡（L224-239）外层 div 设 `maxWidth:'86%', width:'100%'`，但 flex 布局下子项未设 `minWidth:0`，导致 flex 容器无法收缩，内层命令块（L231）的 `overflow:'auto'` 在父级宽度未被约束时不生效，超长单行命令撑破卡片。

**修改**：
- 外层卡片 div 增加 `minWidth:0, overflow:'hidden'`。
- 命令块外层再包一层 `<div style={{maxWidth:'100%', overflow:'auto'}}>`。
- 命令块自身：`whiteSpace:'pre-wrap', wordBreak:'break-all', overflow:'auto', maxHeight:220, maxWidth:'100%', boxSizing:'border-box'`。

**验收**：无论超长单行命令还是多行脚本，均限制在卡片宽度内，超出显示滚动条，卡片不溢出。

---

## 第二节：去掉输出截断（后端 + 前端）

**原则**：只对**用户可见**的输出去截断；进审计库的内部字段保留截断，避免日志库爆炸。

### 后端 `orchestrator.py`
| 位置 | 现状 | 改为 |
|------|------|------|
| `execute_suggestion` L1420 `r.stdout[:500]` | 截断 500 | `r.stdout[:30000]` |
| L1422 `r.stderr[:200]` | 截断 200 | `r.stderr[:10000]` |
| L1432 `return "\n".join(outputs)[:2000]` | 截断 2000 | 全量（不截断） |
| L1373 `final_response[:3000]` | 截断 3000 | 全量 `final_response` |
| L391 `infra_data[:2000]` | 截断 2000 | `[:20000]` |
| L439 `k8sgpt_raw[:2000]` | 截断 2000 | `[:20000]` |
| L416 `trace_data[:3000]` | 截断 3000 | `[:30000]` |

**保留截断（审计内部）**：`_audit_log` 的 `script[:500]`、`output_preview[:200]` 保持不变。

### 前端 `AiChat.tsx`
- L228 `m.plan.slice(0,220)+'…'` → 展示完整 plan（去掉 220 截断）。

**验收**：命令执行输出、plan、最终报告完整展示，无"…"截断。

---

## 第三节：命令执行范围限制（ShellPolicy G/H 黑名单）

**确认范围**：
- ✅ **A-F 允许**：A 只读诊断、B 只读集群、C 受控重启、D 扩缩容、E 打标签、F 删除资源。
- ⛔ **G 禁止**：部署/拉取外部组件。
- ⛔ **H 禁止（默认）**：日志/资源清理。

**实现**：`shell_policy.py` 新增两个黑名单正则集合，在现有白名单校验 `is_whitelisted_for_execute` **通过之后**追加检查。

```
BLOCK_PATTERNS_G（外部部署）:
  helm (install|upgrade|create|add|repo|pull)
  kubectl (apply|create) -f / -k / -R     # 应用外部/批量 manifest
  kubectl apply 管道注入 (curl|wget) .* kubectl
  docker (pull|run|build|push)
  git clone

BLOCK_PATTERNS_H（日志/资源清理）:
  journalctl --vacuum
  rm -rf / rm -r
  truncate
  kubectl delete --all / kubectl delete .* -l    # 批量删除
```

**F（删除资源）与 H 的边界**：删除命令需命中**明确的具体资源名**才放行（如 `kubectl delete pod <明确pod名>`）；含 `--all`、通配符 `-l label` 的批量删除禁止。

**新增方法**：`ShellPolicy.check_extra_blacklist(script) -> Optional[str]`，返回拒绝原因或 None。`execute_suggestion`（`orchestrator.py`）与 `tools.py::execute_shell` 在现有白名单校验后调用它。

**验收**：
- 允许：`kubectl rollout restart deploy/x`、`kubectl scale deploy/x --replicas=3`、`kubectl delete pod <具体名>`、`kubectl get pods -n observability`。
- 拒绝：`helm install`、`kubectl apply -f https://...`、`kubectl delete pod --all`、`rm -rf /tmp`、`journalctl --vacuum`。

---

## 第四节：每次对话采集日志 + 调 k8sgpt + 最终报告按钮

### 4.1 每次 aichat 都采集日志 + 调 k8sgpt（`orchestrator.py::node_collect`）

**日志采集**：
- `tools.py` 新增 `query_logs(service, minutes=30) -> str`：调用 `GET {QUERY_API}/logs/query?service={svc}&minutes={minutes}`，返回 ClickHouse `log_records` 最近日志（含 timestamp/service/severity/body）。
- `node_collect` 在 `red_metrics`/`trace_data` 采集后，**无条件**采集：`result["logs_data"] = query_logs(svc)`（svc 为空时降级为全量或跳过并置空）。
- 上游有 svc 时用 `svc`，无 svc 时采集全局最近日志（`query_logs("")` 走 query-api 全量）。

**k8sgpt**：
- 去掉当前 `if api_key and cfg` 的前置条件，改为**每次无条件尝试调用**；失败快速跳过不阻塞。
- 保留 `-n observability`、`timeout 10s`、stdout 截断提高到 `[:20000]`。
- API key 仍通过临时环境变量传入子进程（不改安全模型）。

**下游注入**：
- 将 `logs_data` + `k8sgpt_raw` 注入 RCA / CrewAI / 最终报告 的 system prompt，使每次分析都结合日志与 K8s 诊断。
- `node_crewai`（L660 附近）system prompt 增加日志与 k8sgpt 数据段（现有已含 infra/alerts/metrics，追加 logs/k8sgpt）。

**延迟说明**：每次对话新增 k8sgpt（约 +10s）与日志查询（约 +1s）。已获用户确认接受。

### 4.2 新增"输出最终版本报告"按钮（前端 + 后端）

**前端 `AiChat.tsx`**：
- `ConfirmCard` 新增第三个按钮 **「输出最终版本报告」**，位于"确认执行/驳回"同排或下方。
- 点击后调用 `finalReport({ session_id: activeSession, service: '' })`，loading 期间按钮 disabled，收到完整报告后追加为一条 assistant 消息（`kind:'report'` 或普通消息）。

**前端 `client.ts`**：
- 新增 `export const finalReport = (data: { session_id?: string; service?: string }) => api.post('/ai/final_report', data)`。

**后端 `main.py`**：新增 `POST /api/v1/ai/final_report`
- 请求体：`{ session_id, service }`。
- 逻辑：
  1. 读取该 session 的历史消息/状态（含最初分析、每轮处置建议、每轮执行输出 `exec_result`、日志、k8sgpt 诊断、最终 metrics 对比）。
  2. 组装完整 prompt → 调 LLM 生成**最终版本报告**：根因 / 处置过程 / 执行结果 / 遗留风险 / 后续建议。
  3. 报告**全量返回**（不截断）。
- 因为 4.1 已保证每次对话采集日志 + 调 k8sgpt，最终报告直接汇总已有 state，不重复采集。

**验收**：点击按钮后生成完整最终报告并展示，涵盖多次处置建议及执行结果、日志、k8sgpt 诊断。

---

## 测试计划

**后端（pytest，隔离 TestClient + 临时 store）**：
- `node_collect` 含 `logs_data` 非空（mock query_logs）与 k8sgpt 被调用（mock subprocess）。
- `ShellPolicy.check_extra_blacklist`：G/H 黑名单用例（允许/拒绝矩阵）。
- `execute_suggestion`：全量输出不截断、白名单 + 黑名单双重校验。
- `final_report` 端点：200、返回完整报告、不截断。

**前端（playwright）**：
- 卡片不溢出、命令块可滚动。
- 命令执行输出完整展示。
- 最终报告按钮可点击并展示报告。

## 部署与同步

1. 后端 pytest 通过后重建 orchestrator 镜像（如 `v1.1.20`）。
2. 前端 build 后重建镜像（如 `v3.4.5`）。
3. `helm upgrade --reuse-values --set orchestrator.image.tag=... --set frontend.image.tag=...` 部署到 namespace=observability。
4. 本地验证后 push 到 GitHub（`Jw-Jm/aiops-edge` main）。

## 不做的事（YAGNI）
- 不重构 LangGraph 图结构。
- 不新增数据库表（复用 session/audit 现有存储）。
- 不改审计日志截断（内部字段）。
- 不自动执行命令（始终人工确认）。
