# Plan: CONTROLLED_AI_INVESTIGATION_CANDIDATE（Phase B2 Chat/Investigation）

依据《AIOps_全面代码修改报告_V2.md》§20 + Phase B2：普通 Chat 不固定采集实时指标/日志/Trace/K8s；
需要实时事实/RCA 的请求必须 `investigation_required -> 用户显式开始调查 -> 创建 Run`。

## Implementation Status（2026-08-25，全部完成 + 真实环境验证）

### C2-1 普通 Chat 无固定实时采集（COMPLETE）
- `node_chat_classify`（B2-03）：纯闲聊/信息查询 → `chat_pure=True`（跳过 heavy collect）；
  普通诊断 → 正常 Chat 轻量链路（保留 exec_context）；仅明确"发起调查/完整根因分析"→ CTA。
- 新增测试 `test_chat_investigation_split.py`（5 passed）验证纯闲聊不采集/诊断不短路/CTA。

### C2-2 live diagnosis → explicit Run（COMPLETE）
- AiChat 识别 `__investigation_required__` → "创建结构化调查 (createRun)"按钮 → `/investigation/new`。
- `NewInvestigation.tsx` POST `/api/v1/ai/runs`（createRun，显式按钮才创建，服务器重鉴权）。

### C2-3 封死 Chat executeSuggestion 写旁路（COMPLETE）
- `AiChat.tsx` `doExecute` **不再调用 `executeSuggestion`**（`/ai/suggestion/execute` 写端点）：
  Chat 不直接触发真实 Action，改为显示"🔒 已阻止 Chat 内脚本执行（C2-3 写旁路封死）…请发起显式调查"。
- 移除 `executeSuggestion` import（tsc exit 0）。

### C2-4 UI Tool activity 只展示真实 ToolRun/Event（COMPLETE + 真实环境）
- query-api 新增 `GetRunToolsPublic`（GET `/api/v1/ai/runs/{id}/tools`，canonical-protected +
  tenant/run ownership，返回真实 `ai_tool_runs` 数据质量字段）。
- `ListByRun` 扩展扫描全部 B1 字段（args_hash/executor/lease_epoch/result_quality/eligible/digest/truncated）。
- 前端 `IntelligentInvestigation.tsx`：`listRunTools` 拉取真实 ToolRun，渲染"工具活动 (真实 ToolRun)"
  卡片（只展示真实 ai_tool_runs，不用图节点/计划步骤推断冒充）。
- **真实环境验证**：public `/tools` 返回真实数据（`tr-c2-1`/query_logs/success/eligible=true/
  executor=orchestrator/lease_epoch=2/quality=complete）。

### C2-5 清理 Orchestrator direct legacy paths（COMPLETE）
- helm `ai-orchestrator/deployment.yaml`：`injectDbCredentials=false`（默认）时**不再注入任何
  MYSQL_*/CLICKHOUSE_* 直连配置**（host/user/db/password 全 gate）——生产 Orchestrator Pod
  无 MySQL/CH 直连凭据，数据面一律经 query-api InternalQueryClient + PersistentRunRepository。
- `helm template` 验证默认无 MYSQL_HOST/CLICKHOUSE_HOST 注入；`helm lint` 0 failed。
- orchestrator 全量测试 1132 passed（无回归）。

## 验证
- query-go 10 包、ingest 6 包、orchestrator 1132、frontend tsc exit 0 + vite build ✓ built。
- 真实环境：query-api v2-c2 部署 Ready；public `/tools` 真实 ToolRun 数据验证通过。

## 边界 / 诚实
- 多节点 failover / MySQL 真 HA 标记 BLOCKED_BY_ENV（单节点）。
- Execution Production Execution=NOT YET APPROVED；红线 F1-F5 保持；GIT_ACTION=NONE。
- 已达到 `RUNTIME_CORRECTNESS_CANDIDATE -> CONTROLLED_AI_INVESTIGATION_CANDIDATE`。
  下一步：`CONTROLLED_ACTION_CANDIDATE`（Phase D 独立受控执行，保持 disabled）。
