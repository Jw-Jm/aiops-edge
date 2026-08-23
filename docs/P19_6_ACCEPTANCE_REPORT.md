# P19.6 十问 + P19.8 Browser E2E — 验收报告（Orbstack Acceptance）

- **STATUS**: 评审裁决 → **P19.6 功能验收通过（安全验收需重跑 N1-N7）；P19.7 真实功能通过（配置持久化未完成）；P19.8 前端 API 已部署（完整 E2E 未完成）；P19.9 代码级通过（Browser Gate 未完成）；Phase 19 整体未完成**
- **日期**: 2026-08-22
- **环境**: orbstack-local-acceptance（observability namespace）
- **红线**: F1-F5 保持；真实执行仍 NOT APPROVED；未触发任何 Action/Approval/Execution/Remediation
- **评审裁决要点**（2026-08-22）:
  - P0 敏感信息清理 + 凭据轮换（见 §1.4）。
  - P0 用户 RBAC 收敛：`SERVICE_ACCOUNT_ROLES` 全域 admin 已移除，改由 query-api 权威角色校验后签名 capability（见 §1.5）。
  - `tenant_clusters` 补写应经 Cluster Registry 规范注册路径（见 §1.6）。
  - `ai.chat 不创建 Run` 已补 DB 前后 Run 数对比证据（见 §3.1）。
  - K8s RBAC 403 使 K8s 健康/OOM/节点压力只能证 unknown-safe，不能证真实 RCA 数据质量（见 §6）。
  - 前端仅"全部集群" → P19.8 为**受限 E2E**，非完整产品 E2E。
  - P19.7 真实功能验证通过（配置持久化未完成）/P19.9 代码级 Tamper 通过（Browser Gate 未完成）+ 真实双集群未接入 → **Phase 19 未完成**。

---

## 1. 部署审计

### 1.1 Secret（QUERY_TO_ORCHESTRATOR 方向凭据）
- 废弃并重新生成（上一私钥视为泄露，不回显/不提交/不写日志）。
- 写入 `aiops-secrets`，更新后 `resourceVersion=594049`。
- 分配最小权限：
  - **query-api** 仅持 `QUERY_TO_ORCHESTRATOR_SIGNING_KEY`（Ed25519 私钥，64 字节 Go 格式）+ `QUERY_TO_ORCHESTRATOR_TOKEN`（方向 service token）。
  - **orchestrator** 仅持 `QUERY_TO_ORCHESTRATOR_VERIFY_KEYS`（验证公钥）+ 预期 issuer=`query-api` / audience=`ai-orchestrator`（`internal_ingress.py` 常量），**不持有私钥**。
- 审计 marker（值哈希，不含值）：已随写入记录；临时密钥文件已清理。

### 1.2 镜像部署（前后版本 + digest + 回滚目标）
| 服务 | 部署前 | 最终部署 tag | 最终部署 digest（pod imageID） | 回滚目标 |
|------|--------|-----------|------------------------------|----------|
| query-api | query-api:v1.1.8-p19-fix | query-api:v1.1.9-p19d-a8fdb5d9 | sha256:9443ff7fd1e337c1eb9132fb891337fb3a00d29a25201e269d8a72c8ae34684c | query-api:v1.1.8-p19-fix |
| ai-orchestrator | ai-orchestrator:v1.1.8-p18-97105ec3 | ai-orchestrator:v1.1.9-p19d-a8fdb5d9 | sha256:c5f1d728a60fdfdeb45019698e532d34eacf39e6f4810fc05c7f6d12fc01563b | ai-orchestrator:v1.1.8-p18-97105ec3 |

> 迭代：query-api p19→p19d（最终含用户 RBAC 权威 SoT）；orchestrator p19→p19b（严格 capability）→p19c（LLM 配置）→p19d（trust 签名上下文，去除 SERVICE_ACCOUNT_ROLES 依赖）。

### 1.3 部署后健康检查
- query-api `/health` = `ok`，issuer 已启用（启动日志无 "issuer disabled"）。
- orchestrator `/health` = `{"status":"ok","version":"5.0","langgraph":true,"sse":true,...}`。

### 1.4 P0 敏感信息清理 + 凭据轮换
- 从 `docs/P19_6_ACCEPTANCE_REPORT.md` 与主 Evidence 移除登录凭据（改"受控测试账号"）与 LLM provider key 片段（明文不落文档/日志）。
- 已轮换 acceptance 账号口令：旧口令（原文档所示）不再有效（登录返回 invalid credentials），全部旧会话已 revoke（active sessions=0）；`ADMIN_INITIAL_PASSWORD` secret 已同步（resourceVersion=596037）。新口令仅通过安全通道告知用户，不落任何文档。

### 1.5 P0 用户 RBAC 收敛（query-api 权威 SoT）
- **移除** `SERVICE_ACCOUNT_ROLES` 部署 env（此前为受控测试账号配置 `{"<uuid>":"admin"}` → 全域 admin，违反 P0）。
- query-api `ProxyChat` 新增 `authorizeUserChatCapability(userID)`：从 MySQL 读取用户**权威角色**（`UserDAO.GetByUUID`），校验其授予 `ai.chat`（admin/engineer/operator/viewer 均可对话），通过才签发 `capability=ai.chat`；否则 fail-closed 403。用户 RBAC 权威来源 = query-api + MySQL，不再是 SERVICE_ACCOUNT_ROLES。
- orchestrator `/internal/v1/chat` 改为 **trust 签名上下文**（`verify_run_invocation_ingress` 已验签），去除对 `SERVICE_ACCOUNT_ROLES` 的 `_authz_matrix.authorize` 依赖，改校验签名上下文字段完整（principal/tenant/cluster 非空 fail-closed）。
- 测试：`TestProxyChatRejectsUserWithoutAIChatRole`（inactive user→403）、`test_chat_missing_principal_rejected`（畸形上下文→403）。

### 1.6 tenant_clusters 规范注册路径
- 规范路径：`ClusterDAO.RegisterCluster` 原子写入 `clusters` + `EnsureTenantCluster`（`INSERT IGNORE INTO tenant_clusters`），幂等且记录 Tenant 1:N Cluster ownership。
- 本轮 canonical cluster 的 `tenant_clusters` 绑定已核验与 RegisterCluster 幂等结果一致（cluster `91771a6e-...` → tenant `7ed01afc-...`，clusters 行 lifecycle=ready）。
- **结论**：后续 tenant→cluster 归属必须走 `RegisterCluster`/注册 API（含来源与审计），不以手工 SQL 修补为惯例。

### 1.7 前端 canonical cluster 选择修复
- 根因：`/api/v1/clusters`（GET）被 `RequireRoleForWrite` 包裹 → 一律 403 → 前端 `refreshClusters` 拉不到集群 → 下拉仅"全部集群"。
- 修复（query-api）：`/api/v1/clusters` 改注册 `handler.ClusterList`（只读），并加入 `isCanonicalProtectedRoute`（JWT+canonical tenant+成员）；写/同步（POST create、sync）仍 `RequireRoleForWrite` fail-closed。补测试 `TestIsCanonicalProtectedRouteQueryEndpoints` 白名单 + `TestAuthMiddlewareFailsClosedForLegacyRoute` 改用仍 fail-closed 的 `/topology/sync-catalog`。
- 部署：`query-api:v1.1.9-p19e-a8fdb5d9`（digest sha256:4f118841...）。前端 `refreshClusters`→`listClusters`→`/clusters` 现在可返回 canonical 集群。

### 1.8 K8s 数据 RBAC 修复
- 根因：query-api `query-api-node-reader` ClusterRole 仅授 nodes 只读，pods/events/deployments 一律 403 → chat 的 K8s 健康/OOM/节点压力查询只能 unknown-safe。
- 修复：`query-api-node-reader` ClusterRole 补 `pods, pods/log, events, namespaces`（get/list/watch）、`apps/deployments,statefulsets,replicasets,daemonsets`（get/list/watch）、`metrics.k8s.io/pods`。`kubectl auth can-i`（as query-api SA）list nodes/pods、get events、list deployments 全部 **yes**。
- 剩余：boundary client `ListPods` 未实现（返回空），pod 级数据仍需接线（后续专项）。

---

## 2. 拒绝性验证（部署环境实测，/internal/v1/chat）

| # | 用例 | 期望 | 实测 |
|---|------|------|------|
| T | valid ai.chat | 200 stream | ✅ 200 |
| N1 | system principal | 403 | ✅ 403 SYSTEM_PRINCIPAL_DENIED |
| N2 | 过期 context | 401 | ✅ 401 invalid_context |
| N3 | `ai.investigate` 冒充 `ai.chat` | 403 | ✅ 403 CAPABILITY_DENIED |
| N4 | 无 capability（缺失） | 403 | ✅ 403 CAPABILITY_DENIED（fail-closed，不允许默认 ai.chat）|
| N5 | body tenant 覆盖签名 scope | 403 | ✅ 403 TENANT_ACCESS_DENIED |
| N6 | body cluster 覆盖签名 scope | 403 | ✅ 403 CLUSTER_ACCESS_DENIED |
| N7 | nonce 重放 | 409 | ✅ 409 CONTEXT_REPLAYED |

**前端 tenant/cluster 身份不能被 body 覆盖**：N5/N6 证明 body 传的 tenant/cluster 与签名 scope 不一致即拒绝——身份以服务端签名上下文为准。

---

## 3. P19.6 十问（前端 JWT → query-api → /internal/v1/chat）

| 问题 | HTTP | SSE 帧 | done | 结果 |
|------|------|--------|------|------|
| 1 K8s 健康状况+证据 | 200 | 10 | ✅ | PASS |
| 2 orders 错误率上涨 | 200 | 41 | ✅ | PASS |
| 3 定位异常日志模式 | 200 | 42 | ✅ | PASS |
| 4 OOMKilled 根因 | 200 | 51 | ✅ | PASS |
| 5 是否与变更有关 | 200 | 37 | ✅ | PASS |
| 6 K8sGPT 结果 | 200 | 37 | ✅ | PASS |
| 7 Runbook/历史案例 | 200 | 60 | ✅ | PASS |
| 8 缺什么证据 | 200 | 37 | ✅ | PASS |
| 9 处置方案（不执行）| 200 | 40 | ✅ | PASS |
| 10 修复后验证恢复 | 200 | 39 | ✅ | PASS |

**10/10 PASS，SSE 流全部完整不中断（均含 done 事件）**。

### 3.1 ai.chat 不创建 Run（DB 前后 Run 数对比证据）
- `aiops.ai_runs` 计数 **chat 前 = 1**。
- 连续发送 2 条 chat（均 HTTP 200，SSE 完整）。
- `aiops.ai_runs` 计数 **chat 后 = 1**（不变）。
- 结论：**ai.chat 未创建任何 Investigation Run**。结合 §2 拒绝性验证 N3（ai.investigate 冒充 ai.chat 403）与独立 capability 设计，证明对话不绕过 `ai.investigate` 的 ManualBoundary。

---

## 4. P19.8 Browser E2E（只读路径）

agent-browser 真实 Chromium 驱动，使用**受控测试账号**登录（凭据不落文档）：

| 路径 | 结果 |
|------|------|
| login | ✅ 成功进入 dashboard |
| AI 运维助手 chat | ✅ 发送 K8s 健康问题 → 真实结构化分析报告（RCA 根因分析/RAG 案例匹配/CrewAI 分析 全 completed），经 query-api→orchestrator→LLM→SSE→前端全链 |
| 调查中心 | ✅ 只读列表（Run 8bf2befd-5dd0-4b2b-b413-c23c52bd699e，status=created），无自动触发 |

- **未触发任何 Action/Approval/Execution/Remediation 入口**（只读调查/聊天路径，符合授权）。
- 前端默认 cluster 需选 canonical cluster（`currentClusterId=91771a6e-...`）；`cluster_id='all'` 时 query-api fail-closed 403（正确）。

---

## 5. 验收标准对照（按评审裁决分级）

| 标准 | 状态 |
|------|------|
| 1. 前端→query-api→/internal/v1/chat 十问均成功，SSE 不中断 | ✅ **功能验收通过**（10/10）|
| 2. 前端 tenant/cluster 身份不能被 body 覆盖 | ✅ **功能通过**（N5/N6 拒绝）|
| 3. ai.chat 不创建 Run、不绕过 ai.investigate 的 ManualBoundary | ✅ **功能通过**（DB 前后 Run 数 1→1，见 §3.1）|
| 4. 负向鉴权用例在部署环境可复现 | ✅ **安全验收=条件通过**（N1-N7 全通过，但 RBAC 权威 SoT 刚收敛，需真实环境复核）|
| 5. 真实执行保持 NOT APPROVED | ✅ 未触发任何执行 |

> **安全验收（标准 4）**：负向用例通过，但 P19.6 原依赖 `SERVICE_ACCOUNT_ROLES` 全域映射（P0），已收敛为 query-api 权威 SoT + trust 签名上下文。**条件通过**：RBAC SoT 收敛为本次新改，需在后续真实环境 Integration Gate 重新执行负向集确认无回归。
>
> **P19.8 产品 E2E（标准 1 的浏览器形态）= 条件通过（受限 E2E）**：前端集群下拉仅"全部集群"，需注入 canonical `currentClusterId` 才能选中单集群；普通用户当前无法完成正常单集群选择（`all` fail-closed）。故 P19.8 记为**受限 E2E**，非完整产品 E2E。

---

## 6. 已知真实环境发现（非本次回归）

- **query-api `/api/v1/kubernetes` 503**（K8s 数据 RBAC 403 根因）：浏览器 chat 报告如实反映"无法评估集群健康（Pods/Nodes HTTP 403）"，数据权限待后端修复（记忆 65949857 既有）。
- **前端集群下拉仅"全部集群"**：`currentClusterId` 未持久化 canonical cluster，需用户选择/注入；`cluster_id='all'` fail-closed 正确。
- **LLM 已连接**：`/settings/llm/internal` 返回已启用 deepseek provider 的配置（API key 明文不落任何文档/日志），chat 不再出现"LLM 未连接"提示；但十问内容受真实数据源可用性影响。
- **多集群/同名服务跨集群比较**：当前 orbstack 仅一个集群（91771a6e-...），`aiops-kind-02` 未接入，跨集群十问无法在本环境实跑（P19.3 已在两集群代码层面验证，真实双集群待后续环境）。

---

## 7. 结论（按评审裁决，统一分层）
- **P19.6 = 功能验收通过**：十问 10/10（SSE 不中断）、chat 全链、ai.chat 不建 Run（DB 前后 1→1）。**安全验收需在 p19d/p19e 最终部署重跑 N1–N7**。
- **P19.7 = 真实功能验证通过**：`k8sgpt analyze --explain` 真实语义正确（deepseek/openai）；**配置持久化与安全注入未完成**（deepseek key 已按可能暴露处理并清理，需用户轮换；后续专项改 Secret 挂载/受控环境变量注入）。
- **P19.8 = 前端 cluster API 修复已部署**（query-api p19e，`/api/v1/clusters` canonical-protected 只读）；**完整产品 E2E 未完成**。
- **P19.9 = P13.2/P19.9 代码级 Tamper 防护通过**（忽略前端 role + query-api 权威角色签名，test_p13_security 17 passed）；**Browser Tamper Integration Gate 未完成**（需三个最小权限受控测试账号，测试后删除/禁用）。
- **Phase 19 整体未完成**：真实双集群（aiops-kind-02）未验收；P19.8 完整产品 E2E 与 P19.9 Browser Tamper 待受控账号登录。
- 红线 F1-F5 保持；真实执行 NOT APPROVED；GIT_ACTION=NONE。
- **下一步**：真实双集群接入（另行给出接入范围/租户归属/数据源/回滚与隔离 Gate 后授权）；P19.8/P19.9 用三个最小权限受控测试账号（用户浏览器登录或受控 secret 注入），测试后删除/禁用；k8sgpt 配置持久化安全专项（Secret 挂载）。不得在聊天中索取/提供口令。
