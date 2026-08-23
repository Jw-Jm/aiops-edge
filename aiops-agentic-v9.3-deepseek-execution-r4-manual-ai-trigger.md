# AIOps Agentic 全面重构最终强约束执行规格书
## V9.3 DEEPSEEK EXECUTION R4 — Phase 7+ Manual-Triggered Multi-Source AIOps Execution Contract

> **项目根目录**：`/Users/mssc/Documents/Code/agent/aiops/`
> **执行对象**：DeepSeek 代码执行代理
> **实施范围**：整个 AIOps 项目，不局限于 AI Orchestrator
> **执行模式**：Phase 1–6 仅按 `aiops-agentic-v9.2-final-r2.md` 执行；V9.3 仅在 V9.2 Gate 6 PASS 后激活，并从 Phase 7 开始严格顺序推进
> **Git 规则**：整个实施过程禁止 `git add`、禁止 `git commit`、禁止 `git push`
> **数据删除规则**：Phase 17 之前禁止任何未经单独授权的历史运行数据物理删除
> **最终目标**：建设“面向云原生与边缘基础设施的全栈智能运维分析与处置平台”。平台作为统一智能运维处理中心，统一接入平台自身及已注册的其他 Kubernetes 平台及其关联基础设施的多源运行数据；所有 AI 分析必须由已认证用户人工显式触发。用户发起 AI 调查后，由 AI Agent 在该 Run 内自动完成跨指标、日志、链路、Kubernetes、基础设施及变更的证据收集与关联分析，定位故障根因，生成安全、可审计的处置方案，并通过授权执行与效果验证形成“人工发起调查—定位根因—安全处置—验证恢复”的智能运维闭环。
> **V9.3 整理日期**：2026-08-20
> **V9.3 整理范围**：V9.2 FINAL R2 独占 Phase 1–6 的执行与 Gate；V9.3 不重写、不补测、不追溯修订 Phase 1–6。V9.3 只在 V9.2 Gate 6 PASS 后从 Phase 7 接管，继承 Gate 6 的最终系统状态，并细化 Phase 7–21 的 DeepSeek 执行方案；同时补充最小化“平台自身 + 已注册外部 Kubernetes/基础设施数据源”接入边界，并冻结“所有 AI 分析必须由人工显式触发”的调用边界。除真实代码无法表达既定目标外，不新增 Incident Engine、Detection Engine、边缘自治/治理、Learning Engine 或新的授权模型。

---

# 零、V9.3 文档地位、决策优先级与当前状态

## 0.1 文档地位与合同切换边界

本文件是 `aiops-agentic-v9.2-final-r2.md` 的 **Phase 7+ 后继执行合同**，不是 V9.2 Phase 1–6 的替代版。

合同边界固定为：

```text
Phase 1–6
→ ONLY aiops-agentic-v9.2-final-r2.md
→ V9.3 无执行权、无补充 Gate 权、无追溯重跑权

V9.2 Gate 6 PASS
→ 形成不可追溯修改的 V9.2_BASELINE_AFTER_GATE6
→ 激活 V9.3

Phase 7–21
→ aiops-agentic-v9.3-deepseek-execution-r4-manual-ai-trigger.md
→ 继承 V9.2 已冻结架构与 Gate6 退出状态
```

V9.3 允许在 Phase 7+ 为新能力继续修改 query-api、orchestrator、frontend 等已有代码，但这些修改属于**后续 Phase 的增量实现**，不得被解释为“回到 Phase 1–6 重新执行”或“修改 V9.2 Gate 结论”。所有后续修改必须持续保持 V9.2 Gate 1–6 的安全、数据 SoT、授权、Cluster Identity、Writer/Reader 与 Trusted Boundary 不变量。

从 V9.3 激活后，Phase 7–21 的冲突优先级为：

```text
1. 用户最新明确拍板的决定
2. 本 V9.3 R4 明确标记为 V9.3_FROZEN / V9.3_APPROVED 的 Phase 7+ 条款
3. V9.2 FINAL R2 中标记为 FROZEN / APPROVED / PASS 的条款及 Gate 1–6 最终退出状态
4. 已通过测试和 Gate 的真实生产代码行为
5. 当前 Phase 7+ 的经批准实施计划
6. 原 V9.2/R2 未被 V9.3 在 Phase 7+ 明确修订的条款
7. 更早的 V1–V9.1 草稿、讨论稿和过渡建议
```

**特别规则：任何 V9.3 条款都不得反向覆盖 V9.2 Phase 1–6。** 如果某个 Phase 7+ 新需求需要修改前期已产生的代码，只能作为当前 Phase 的增量修改实施，并必须保持 Gate 6 基线不退化。

## 0.2 R2 已合并的权威修订

```text
INTERNAL-AUTH-P0-011
Context signing:
HS256 → EdDSA / Ed25519

JWS header:
alg=EdDSA
typ=AIOPS-CONTEXT
kid=<key-id>

Key ownership:
每个调用方向使用独立 Ed25519 keypair；
签发方持有 private key；验证方只持有对端 public key。
```

其他已冻结修订：

1. `RoleScopeAssignment` 是逻辑实体，当前物理表沿用并演进 `scope_assignments`；禁止再建平行 `role_scope_assignments` 表。
2. Phase 3 当前 session/token-version 单一权威源是 `user_sessions.token_version`。Phase 4 若切换到目标名 `auth_sessions`，必须通过版本迁移和原子 runtime cutover 完成；任何时刻不得出现两个 token-version authority。
3. `tenant_clusters` 表达 Tenant 1:N Cluster，且 `cluster_id` 唯一，V1 中一个物理 Cluster 只有一个 owning Tenant。
4. `credential_ref` 不只是注册表字段，必须经正式 Kubernetes Access Boundary 解析 Secret，并绑定真实 Kubernetes identity。
5. Kubernetes identity authority 固定为目标集群 `kube-system` Namespace 的 `metadata.uid`；API 地址、context、node name、slug 只能用于诊断，不能作为身份权威。
6. query-api 与 orchestrator 的生产可信协议只允许三类 Context：`RunInvocationContext`、`RunControlContext`、`TrustedRequestContext`。旧 signed `RequestContext`、`typ=JWT` 及 legacy signer/verifier 已退出生产协议。
7. Phase 4 的 MySQL migration metadata 使用专用 `aiops_schema_migrations`，不复用或改造旧 `schema_migrations`。
8. `RequireCurrentVersion` 必须只读校验 migration id 与 checksum；missing 和 checksum mismatch 均 fail closed。
9. MinIO-compatible Object Store 仍是 large Evidence 与 Knowledge object 的冻结 SoT。没有正式 Architecture Erratum，不得静默移除。

## 0.3 V9.3 激活条件

V9.3 在以下条件全部成立前状态固定为：

```text
V9.3_STATUS = NOT_ACTIVE
NEXT_PHASE = NOT_STARTED
```

唯一激活条件：

```text
V9.2 FINAL R2
Phase 1 PASS
Phase 2 PASS
Phase 3 PASS
Phase 4 PASS
Phase 5 PASS
Phase 6 PASS
Gate 6 PASS
GIT_ACTION = NONE
```

DeepSeek 不得使用 V9.3 去指导、补充或完成 V9.2 Phase 6。V9.2 Gate 6 没有正式 PASS 时，只能继续执行 V9.2。

## 0.4 V9.2_BASELINE_AFTER_GATE6

V9.3 激活时必须读取并登记 V9.2 Phase 6 最终报告，但**只读取结果，不重新执行 Gate 1–6**。Activation Record 至少记录：

```text
source_contract = aiops-agentic-v9.2-final-r2.md
phase_1_to_6 = PASS
gate_6 = PASS
phase6_report_ref = <actual report/path>
git_action = NONE
```

同时确认 V9.2 Gate 6 的关键退出状态仍为：

```text
new writer ACTIVE
new reader ACTIVE
old writer ABSENT
old reader ABSENT
old active adapter ABSENT
production fallback ABSENT
old physical historical data may remain but is unreachable
frontend/query/tool fact semantics consistent
no_data != permission_denied
unavailable != no_data
timeout != generic network error
```

以及 V9.2 已冻结且 Phase 7+ 必须持续保持的基础不变量：

```text
MySQL = current Authorization SoT
canonical Cluster UUID = cluster identity
no default tenant / default cluster
RunInvocationContext / RunControlContext / TrustedRequestContext only
Service Credential != context signing key
orchestrator / Agent no direct target-K8s credential path
Raw Metrics SoT = VictoriaMetrics
Raw Logs SoT = VictoriaLogs
ClickHouse role unchanged
AI Runtime persistence owner = query-api
runtime service no schema DDL authority
WAL / recovery / precheck / audit preserved
```

如果 Activation Record 与 V9.2 Gate 6 报告不一致：

```text
V9.3_STATUS = BLOCKED_ACTIVATION
Phase 7 = NOT_STARTED
```

不得通过 V9.3 自行“补齐”Gate 6 后再宣称激活；应回到 V9.2 合同处理。

## 0.5 V9.3 激活后的唯一下一步

激活成功后：

```text
CURRENT_CONTRACT = V9.3 R4
CURRENT_PHASE = 7
Phase 1–6 = FROZEN_HISTORY_FROM_V9.2
Phase 7 = NOT_STARTED / NEXT
```

V9.3 不维护 Phase 1–6 的新任务树、新 Gate 或新状态。

---

# 一、本文件的执行地位

本文件不是建议、参考架构、设计草稿，也不是供 DeepSeek 自由发挥的需求描述。

本文件从 **Phase 7 起**是本次 AIOps Agentic 全面重构的唯一后继执行合同；Phase 1–6 的唯一执行合同仍是 V9.2 FINAL R2。

DeepSeek 必须遵循以下基本原则：

1. Phase 1–6 的实现与 Gate 只由 V9.2 FINAL R2 定义，V9.3 不追溯修改。
2. V9.2 Gate 6 PASS 是 V9.3 的唯一激活门。
3. V9.3 激活后，DeepSeek 的角色固定为“严格实施者/验证者”。
4. Phase 7+ 不得重新选择 V9.2 已冻结的 Tenant 模型、授权模型、Cluster Identity、数据存储、Writer/Reader SoT、Trusted Boundary；也不得重新设计 Tool、Agent、RCA、Execution 的已冻结总体架构。
5. 如果真实代码路径与本文写法不同，只允许映射真实路径，不允许改变架构目标。
6. 如果现有代码无法满足某条强约束，必须输出 `BLOCKED` 或明确偏差，不得自行采用另一套架构。
7. 不得因为现有实现复杂、测试失败、环境组件缺失，而偷偷降低安全要求或验收标准。
8. 不得用 Mock、Demo、静态文字、前端假数据冒充最终生产能力。
9. 不得在未通过 Gate 时继续下一个 Phase。
10. 任一未完成项必须如实标记，禁止"基本完成""大体通过"后宣称全部完成。

---

# 二、项目实施范围

必须覆盖以下五个自研运行服务：

```text
observability-frontend
ai-apm-query-go
ai-apm-ingest-go
ai-event-collector
ai-orchestrator
```

同时覆盖与其实际运行和交付直接相关的：

```text
MySQL
ClickHouse
VictoriaMetrics
VictoriaLogs
ChromaDB
MinIO
DeepFlow
Kubernetes
K8sGPT
Grafana integration
Helm
Docker
deployment scripts
authentication
authorization
RBAC
knowledge base
workflow
resource graph
LLM configuration
testing
browser E2E
runtime image
```

禁止只改 `ai-orchestrator` 后声称完成"全面重构"。

---

# 三、产品最终形态

当前系统不得继续以：

```text
AI Chat
AI Tool
Workflow
Knowledge Graph
K8sGPT
RAG
Grafana
容量预测
报告
专业页面
```

作为多个彼此独立、需要用户手动组合的功能中心。

最终产品必须收敛为两条任务旅程。

## 3.1 旅程 A：从问题到根因

```text
用户/告警/专业页面
→ 明确 tenant
→ 明确 canonical cluster
→ 明确 resource
→ 明确 time range
→ Intent
→ Planner
→ Investigation DAG
→ Domain Agents
→ Tools
→ ToolResult
→ Evidence
→ Hypothesis
→ Contradiction
→ Missing Evidence
→ Follow-up Investigation
→ Root Cause Ranking
→ Confidence
→ Unknowns
```

用户不得被要求手动选择：

```text
K8sGPT
RAG
Tool
Agent
Workflow
LangGraph
MCP
```

## 3.2 旅程 B：从根因到恢复

```text
Root Cause
→ Structured OpsAction
→ Authoritative Risk Evaluation
→ Current Authorization
→ Confirmation / Approval
→ Precheck
→ Execute
→ Observation Window
→ Verification
→ Success / Partial / Failed / Regressed
→ Run Completion / Verification Result
```

命令成功绝不能等价于业务恢复。

---

# 四、最终唯一生产主链

最终只允许存在：

```text
Browser
  ↓
JWT Identity
  ↓
query-api
  ↓
MySQL Real-time Authorization
  ↓
Tenant Scope
  ↓
Canonical Cluster Resolution
  ↓
RunInvocationContext
  ↓
ai-orchestrator
  ↓
Intent
  ↓
Planner
  ↓
Investigation DAG
  ↓
Domain Agents
  ↓
Tool Registry
  ↓
TrustedRequestContext
  ↓
query-api Trusted Data Boundary
  ↓
ToolResult
  ↓
Evidence Hub
  ↓
Hypothesis RCA
  ↓
Missing Evidence Investigation
  ↓
Root Cause Ranking
  ↓
Structured OpsAction
  ↓
Authoritative Risk Evaluation
  ↓
MySQL Real-time Authorization
  ↓
Confirmation / Approval
  ↓
Execution Adapter
  ↓
Verification
  ↓
Run Completion / optional human-reviewed Knowledge update
```

最终禁止并存：

```text
旧 AI Chat 调查主链
旧 prompt-only RCA
旧 Tool Router
旧 Workflow 调查主链
旧 Session 业务历史
旧 Checkpoint 业务历史
旧 Schema Adapter
默认 Tenant fallback
默认 Cluster fallback
JWT role/scope 权威授权
客户端 tenant/cluster 授权语义
```

---

# 五、全局编码与数据格式规范

所有 JSON API：

```text
UTF-8
Content-Type: application/json
字段命名：snake_case
```

所有业务 UUID：

```text
RFC 4122 textual UUID
```

MySQL V1 UUID：

```text
CHAR(36)
```

不引入 `BINARY(16)`，避免增加第一版复杂度。

所有 API 时间：

```text
UTC
RFC3339 / RFC3339Nano
```

数据库业务时间：

```text
TIMESTAMP(6)
```

禁止依赖操作系统本地时区进行运行逻辑判断。

---

# 六、Tenant 模型完全冻结

## 6.1 必须是真多 Tenant Schema

当前部署虽然只初始化一个 Tenant，但 Schema、Authorization Engine、Run、Evidence、Action、Audit 从第一天必须支持多个 Tenant。

当前默认 Tenant：

```text
tenant_id = generated UUID
slug      = default
name      = Default Tenant
```

禁止：

```text
tenant_id=1
id=1 → default
tenant_id missing → 1
tenant_id missing → default
NULL tenant → default
global current tenant
singleton tenant implementation
```

## 6.2 User 与 Tenant 多对多

User 身份全局唯一。

固定关系：

```text
users
tenants
user_tenants

roles
permissions
role_permissions
scope_assignments   # RoleScopeAssignment logical entity

clusters
tenant_clusters
```

禁止：

```text
Tenant A 创建 user-x
Tenant B 再创建另一个 user-x
```

## 6.3 Cluster 单 Tenant 归属

V1 固定：

```text
User N:M Tenant
Tenant 1:N Cluster
```

`tenant_clusters.cluster_id` 必须唯一。

同一个 canonical Cluster 同一时刻不能属于多个 Tenant。

未来如有共享 Cluster 需求：

```text
新增 cluster_access_grants
```

不得通过复制 Metrics、Logs、Trace 等原始数据实现。

---

# 七、授权模型完全冻结

授权粒度：

```text
tenant
→ cluster
→ namespace
→ resource
→ action
```

V1 授权算法：

```text
DEFAULT DENY
ALLOW GRANT ONLY
NO EXPLICIT DENY RULE IN V1
```

不实现复杂 deny precedence。

授权顺序固定：

```text
1. user 是否 active
2. session 是否 valid
3. user 是否属于 requested tenant
4. requested cluster 是否属于 tenant
5. 是否存在匹配的 RoleScopeAssignment（当前物理表 `scope_assignments`）
6. role 是否包含 requested permission
7. namespace/resource scope 是否匹配
8. capability 是否允许
9. risk/action policy 是否允许
```

Tenant scope grant 可以向下级 Cluster/Namespace/Resource 生效，但只对 Role 明确拥有的 Permission 生效。

例如：

```text
role=viewer
scope=tenant-A
permission=logs.read
```

只能让用户读 tenant-A 允许 Cluster 的日志。

绝不能自动获得：

```text
execute
approve
admin
```

---

# 八、JWT 与 Session 完全冻结

JWT 只允许：

```text
sub
sid
iat
exp
iss
aud
token_version
```

JWT 禁止包含：

```text
role
roles
scope
permissions
tenant_ids
cluster_ids
allowed_clusters
allowed_namespaces
is_admin
action_permissions
```

MySQL 必须存在且只能存在一个权威 Session/token-version authority。逻辑实体记为 `AuthSession`；物理表名必须服从已经通过 Gate 的真实实现，不得因目标命名自行新建第二 authority：

```text
current authority = user_sessions.token_version
OR
versioned migration + atomic runtime cutover completed → auth_sessions.token_version

NEVER BOTH
```

最低逻辑字段：

```text
session_id
user_id
created_at
expires_at
revoked_at
token_version
last_seen_at
```

授权规则：

```text
revoked_at != NULL → DENY
expires_at expired → DENY
user disabled → DENY
token_version mismatch → DENY
```

V1 禁止引入 Authorization Cache。

以下访问必须实时查 MySQL：

```text
public API
Tool call
Run deep link
Evidence deep link
Approval
Confirmation
Execution
Verification
Internal query
```

---

# 九、Cluster Identity 完全冻结

`clusters` 最低字段：

```text
cluster_id
slug
name
type
environment
region
status
version
capabilities
labels
credential_ref
created_at
updated_at
deleted_at
```

含义固定：

```text
cluster_id = immutable identity
slug       = human-readable reference
name       = display
```

slug regex：

```text
^[a-z][a-z0-9-]{1,62}[a-z0-9]$
```

active Cluster slug 全局唯一。

以下均不得替代 canonical `cluster_id`：

```text
slug
name
Kubernetes UID
kube-context
API endpoint
array index
id=1
```

Cluster 删除再注册：

```text
MUST create new UUID
```

即使复用原 slug，也不得复用历史 UUID。

---

# 十、Canonical Resource Identity 完全冻结

Resource ID 不包含 `tenant_id`。

tenant 是 ownership / authorization / isolation 维度，不是资源固有身份。

固定形式：

```text
service:<cluster_uuid>:<namespace>:<service>
deployment:<cluster_uuid>:<namespace>:<deployment>
statefulset:<cluster_uuid>:<namespace>:<statefulset>
daemonset:<cluster_uuid>:<namespace>:<daemonset>
pod:<cluster_uuid>:<namespace>:<pod>
node:<cluster_uuid>:<node>
```

数据库记录必须同时有：

```text
tenant_id
cluster_id
resource_id
```

Resource Resolver 必须是确定性代码。

禁止：

```text
LLM 生成 Resource ID
LLM 推断 cluster
LLM 推断 namespace
```

---

# 十一、内部可信调用分成三个 Context

不能只有一个泛化 `TrustedContext`。

必须严格区分：

```text
RunInvocationContext
RunControlContext
TrustedRequestContext
```

---

## 11.1 RunInvocationContext

方向：

```text
query-api → orchestrator
```

唯一用途：

```text
创建新 Run
```

字段：

```text
version
context_type=run_invocation

issuer
audience

request_id

principal_type
principal_id
session_id nullable

tenant_id
cluster_scope

source

issued_at
expires_at
nonce
```

禁止：

```text
roles
permissions
allowed_clusters
is_admin
credentials
```

`cluster_scope` 只是这次 Run 的目标 Cluster，不是该用户所有 Cluster 权限。

---

## 11.2 RunControlContext

方向：

```text
query-api → orchestrator
```

用于已存在 Run 的控制行为。

字段：

```text
version
context_type=run_control

issuer
audience

request_id
run_id
operation

principal_type
principal_id
session_id nullable

tenant_id

action_id nullable
decision_id nullable

issued_at
expires_at
nonce
```

operation 固定：

```text
cancel
stream
action_decision
```

禁止使用 RunInvocationContext 代替已有 Run 的控制请求。

---

## 11.3 TrustedRequestContext

方向：

```text
orchestrator → query-api
```

字段：

```text
version
context_type=trusted_request

issuer
audience

request_id
run_id

principal_type
principal_id
session_id nullable

tenant_id

scope_kind
cluster_id nullable

capability
source

issued_at
expires_at
nonce
```

`scope_kind`：

```text
cluster
run
```

### cluster scope

用于：

```text
metrics
logs
traces
alerts
topology
kubernetes
changes
knowledge
execution
verification
```

必须：

```text
cluster_id != NULL
```

一个 Context 只能有一个 Cluster。

### run scope

只允许：

```text
control_plane.*
```

必须：

```text
cluster_id=NULL
```

禁止用 run-scope Context 查询 Observability 或执行 Kubernetes Action。

---

# 十二、Principal 模型

固定：

```text
principal_type=user
principal_type=system
```

User principal：

```text
session_id MUST NOT NULL
```

System principal：

```text
session_id MUST NULL
```

V9.3 Phase 7+ 人工 AI 触发覆盖规则（`V9.3_FROZEN`）：

```text
principal_type=system
→ MAY serve internal service/control-plane operations
→ MUST NOT create or start a new AI analysis Run
→ MUST NOT trigger Planner / Agent / LLM / K8sGPT analysis

AI analysis Run creation
→ principal_type MUST be user
→ session_id MUST NOT NULL
→ MUST originate from an explicit authenticated human action
```

V9.2 中关于 `alert-investigator` / `system-triggered run = read_only` 的旧设计，从 V9.3 激活（Phase 7）开始不再作为 AI 调查入口。System Principal 权限仍来自 MySQL/Policy，但仅用于已有内部服务调用、控制面或非 AI 后台处理；不得以“只读”为理由自动启动告警分析、根因分析或其他 AI 调查。

---

# 十三、Service Identity

query-api 与 orchestrator 内部调用固定使用：

```text
Service Credential
+
Signed Context
```

当前不引入：

```text
SPIFFE
Service Mesh
完整 mTLS PKI
```

必须实现抽象：

```text
ServiceAuthenticator
```

四份内部 Secret 逻辑分离：

```text
query-api → orchestrator service credential
orchestrator → query-api service credential

query-api → orchestrator signing key
orchestrator → query-api signing key
```

不得：

```text
两个方向共用 token
service credential 与 context signing key 共用
```

Secret 只通过 Kubernetes Secret 注入。

不得存数据库明文。

---

# 十四、Context 签名

V1 固定：

```text
JWS Compact Serialization
EdDSA / Ed25519
```

每个方向使用独立 Ed25519 keypair：

```text
query-api → orchestrator
query-api holds private key
orchestrator holds query-api public key only

orchestrator → query-api
orchestrator holds private key
query-api holds orchestrator public key only
```

禁止：

```text
两个方向复用同一 keypair
验证方持有对端 private key
service credential 与 signing key 共用
恢复 HS256 作为长期兼容协议
```

JWS Header：

```text
alg=EdDSA
typ=AIOPS-CONTEXT
kid=<key-id>
```

默认 TTL：

```text
60 seconds
```

Clock Skew：

```text
max 30 seconds
```

每次 Tool Call 必须重新签发 Context。

禁止整个 15 分钟 Run 共用一个 Context。

Go 与 Python 必须对相同 fixture 双向互验：

```text
Go sign → Python verify
Python sign → Go verify
```

旧 `typ=JWT` internal context 必须拒绝，不能自动降级到 legacy verifier。

---

# 十五、防重放

每个 Context 必须有 nonce。

入站服务必须维护 replay record：

```text
internal_request_nonces

issuer
nonce
expires_at
```

唯一：

```text
(issuer, nonce)
```

有效期：

```text
context TTL + max clock skew
```

重复：

```text
CONTEXT_REPLAYED
```

必须拒绝。

网络层 retry 必须重新生成：

```text
request_id/nonce/context
```

---

# 十六、Capability 固定

内部 capability V1：

```text
observability.metrics.read
observability.logs.read
observability.traces.read
observability.alerts.read
observability.topology.read

kubernetes.resources.read
kubernetes.events.read
kubernetes.logs.read

changes.read
knowledge.search

control_plane.run.read
control_plane.run.write
control_plane.event.write

execution.precheck
execution.execute
execution.verify
```

禁止临时创造：

```text
logs.query2
cluster.read_all
admin_anything
```

等未定义 capability。

---

# 十七、credential_ref

每 Cluster 一个 Secret。

MySQL 只保存：

```text
k8s-secret://<namespace>/<secret-name>
```

首选 Secret key：

```text
kubeconfig
```

禁止数据库保存：

```text
kubeconfig
token
client key
certificate private material
```

只有 Kubernetes Access Boundary 能 resolve。

orchestrator 永远不得收到 credential 内容。

---

# 十八、Kubernetes Access Boundary

只读路径：

```text
Agent
→ Tool Registry
→ InternalQueryClient
→ query-api
→ Kubernetes Read Adapter
→ ClusterClientManager
→ Kubernetes API
```

写路径：

```text
OpsAction
→ Execution Policy Engine
→ MySQL Authorization
→ Confirmation/Approval
→ Execution Adapter
→ ClusterClientManager
→ Kubernetes API
```

Agent、Planner、Tool 禁止：

```text
创建 Kubernetes Client
加载 kubeconfig
执行 kubectl
读取 Secret
直接访问目标 Cluster API
```

Execution Adapter V1：

```text
存在于 ai-apm-query-go 进程内
```

但必须是独立安全模块。

不新增 Execution 微服务。

---

# 十九、ClusterClientManager

Client Cache Key：

```text
canonical cluster_id
```

禁止：

```text
slug
name
current cluster
default cluster
```

Cache entry 至少记录：

```text
cluster_id
credential_ref
credential_generation_or_hash
client
created_at
last_used_at
```

以下情况立即 invalidation：

```text
credential_ref changed
credential rotation
cluster disabled
cluster deleted
```

禁止跨 Cluster Client reuse。

---

# 二十、K8sGPT

K8sGPT 是：

```text
Kubernetes Agent 的一个 Tool
```

不是 Agent。

如果 K8sGPT 需要 kubeconfig：

1. 只能由 Kubernetes Access Boundary 创建临时 kubeconfig。
2. 文件权限必须 `0600`。
3. 只能包含当前 canonical Cluster。
4. K8sGPT 完成后立即删除。
5. 禁止写日志。
6. 禁止传给 orchestrator。
7. ToolResult 只能返回结构化诊断。

Phase 1 如果发现已有 K8sGPT，优先复用现有固定方式。

如果未发现：

- 只允许使用项目现有锁定 artifact / offline cache；
- 不允许自行 brew/apt 安装最新版本。

无法找到受控 artifact：

```text
BLOCKED_K8SGPT_ARTIFACT_MISSING
```

---

# 二十一、存储职责

最终固定：

```text
VictoriaMetrics
= Raw Metrics Source of Truth

VictoriaLogs
= Raw Logs Source of Truth

ClickHouse
= Trace / Span
+ RED
+ Topology
+ Alert
+ Resource Event
+ Change Record
+ LogPattern / Derived Analytics

MySQL
= Users / Sessions / RBAC
+ Tenant / Cluster Registry
+ Platform Config
+ AI Runtime / Control Plane

MinIO
= Large Evidence Object
+ Knowledge Object

ChromaDB
= Knowledge Vector Index
```

禁止：

```text
raw logs full copy → VictoriaLogs + ClickHouse
```

禁止为了统一数据库，把 Metrics/Logs 全部搬到 ClickHouse/MySQL。

---

# 二十二、查询安全边界

所有 orchestrator → query-api 调查查询必须传：

```text
结构化 Query Request
```

禁止传：

```text
raw SQL
raw PromQL
raw LogsQL
raw kubectl
raw shell
```

query-api 服务端必须注入：

```text
tenant_id
cluster_id
authorized resource scope
time range
```

客户端 body 中如包含 cluster/tenant：

```text
必须与 TrustedRequestContext 完全一致
```

不一致：

```text
CONTEXT_SCOPE_MISMATCH
```

不得使用 body 覆盖可信 Context。

---

# 二十三、多 Cluster Run

Run 支持：

```text
single_cluster
multi_cluster
```

`ai_runs` 最低字段：

```text
run_id
request_id

tenant_id

principal_type
principal_id
session_id nullable

scope_kind
primary_cluster_id nullable

intent
action_mode

target_type
target_resource_id nullable

time_range_start
time_range_end

status
state_version

parent_run_id nullable

created_at
updated_at
finished_at nullable
```

新增：

```text
ai_run_clusters
```

字段：

```text
run_id
cluster_id
```

唯一：

```text
(run_id, cluster_id)
```

single_cluster：

```text
exactly 1 cluster
primary_cluster_id = that cluster
```

multi_cluster：

```text
>=2 clusters
primary_cluster_id=NULL
```

---

# 二十四、多 Cluster 强隔离规则

以下对象必须：

```text
cluster_id NOT NULL
```

包括：

```text
ai_tool_runs
ai_evidence
ai_hypotheses
ai_actions
ai_verifications
```

PlanStep：

```text
aggregate planning step → cluster_id nullable
tool execution step      → cluster_id NOT NULL
```

跨 Cluster Comparison 实现：

```text
Cluster A investigation
+
Cluster B investigation
+
authorized server-side comparison
```

禁止：

```text
一个 Tool 同时 query A+B
一个 Evidence 属于多个 Cluster
一个 Hypothesis 混用 A/B Evidence
```

Multi-cluster Run 只用于 comparison/investigation。

如果要执行写动作：

```text
必须从明确 Cluster 派生新的 single-cluster remediation run
```

禁止在 multi_cluster Run 直接执行。

---

# 二十五、AI Runtime 数据所有权

业务语义 Owner：

```text
ai-orchestrator
```

MySQL Persistence Owner：

```text
ai-apm-query-go
```

orchestrator 禁止直接维护 MySQL Repository。

固定表：

```text
ai_runs
ai_run_clusters
ai_plan_steps
ai_tool_runs
ai_evidence
ai_hypotheses
ai_actions
ai_verifications
ai_approval_decisions
ai_run_events
ai_audit_events
```

平台自身：

```text
platform_audit_events
```

独立。

链路固定：

```text
orchestrator
→ /internal/v1/control-plane/...
→ query-api
→ MySQL
```

---

# 二十六、AI Runtime 事务模型

`ai_runs.state_version`：

```text
BIGINT
```

状态更新必须 optimistic CAS：

```text
UPDATE ...
WHERE run_id=?
AND state_version=expected
```

成功：

```text
state_version + 1
```

失败：

```text
RUN_STATE_CONFLICT
```

禁止 last-write-wins。

---

# 二十七、Idempotency

Run 创建：

```text
tenant_id
principal_type
principal_id
request_id
```

组合逻辑必须幂等。

重复请求返回原 Run。

Write Action：

```text
action_id
idempotency_key
```

写操作 timeout：

```text
write_action_retry = 0
```

必须先查：

```text
execution record
resource state
idempotency state
```

再决定下一步。

---

# 二十八、固定枚举

## 28.1 OpsIntent.intent

```text
query
health_check
diagnose
root_cause_analysis
knowledge_search
capacity_analysis
remediation
execute
verify
```

## 28.2 action_mode

```text
read_only
plan_only
execute_allowed
```

## 28.3 target_type

```text
cluster
namespace
node
service
deployment
statefulset
daemonset
pod
container
workload
host
vm
alert
trace
resource
```

## 28.4 ToolResult.status

```text
success
partial
no_data
failed
timeout
unavailable
permission_denied
```

## 28.5 PlanStep.status

```text
pending
ready
running
success
partial
no_data
failed
timeout
unavailable
permission_denied
cancelled
skipped
```

## 28.6 Hypothesis.status

```text
candidate
supported
confirmed
rejected
unknown
```

## 28.7 Verification.verdict

```text
success
partial
failed
regressed
unknown
```

## 28.8 Risk

```text
R0
R1
R2
R3
R4
```

## 28.9 Run.status

```text
created
planning
investigating
awaiting_confirmation
awaiting_approval
executing
verifying
success
partial
failed
regressed
cancelled
```

禁止创建：

```text
done
complete
completed
finished
succeeded
error
```

等同义状态。

---

# 二十九、Run 状态机

允许：

```text
created
→ planning
→ cancelled
→ failed
```

```text
planning
→ investigating
→ failed
→ cancelled
```

```text
investigating
→ awaiting_confirmation
→ awaiting_approval
→ success
→ partial
→ failed
→ cancelled
```

```text
awaiting_confirmation
→ executing
→ cancelled
→ failed
```

```text
awaiting_approval
→ executing
→ cancelled
→ failed
```

```text
executing
→ verifying
→ failed
```

副作用已经发生后，禁止直接 Cancel 并隐藏真实状态。

必须进入：

```text
verifying
```

或明确：

```text
failed
```

Verification：

```text
verifying
→ success
→ partial
→ failed
→ regressed
```

Terminal：

```text
success
partial
failed
regressed
cancelled
```

Terminal Run 不得重新启动。

---

# 三十、Tool Registry

最低生产 Tool：

```text
query_metrics
query_logs
query_traces
query_alerts
query_topology
query_k8s
k8sgpt_diagnose
knowledge_search
query_changes
execute_k8s
```

保留：

```text
execute_shell
```

但固定：

```text
risk=R4
planner_selectable=false
automatic=false
```

ToolDefinition：

```text
name
category
description
read_only
risk_level
capability
availability
input_schema
output_schema
timeout_class
```

Planner 只能选择 Registry 中已注册、当前 Cluster 可用、当前 Principal 有 capability 的 Tool。

---

# 三十一、Tool 与 Capability 映射

```text
query_metrics
→ observability.metrics.read

query_logs
→ observability.logs.read

query_traces
→ observability.traces.read

query_alerts
→ observability.alerts.read

query_topology
→ observability.topology.read

query_k8s
→ kubernetes.resources.read / kubernetes.events.read / kubernetes.logs.read

k8sgpt_diagnose
→ kubernetes.resources.read

knowledge_search
→ knowledge.search

query_changes
→ changes.read

execute_k8s
→ execution.*

execute_shell
→ execution.*
```

Planner/LLM 不得修改映射。

---

# 三十二、ToolResult

字段：

```text
tool_name
cluster_id

status
summary
data

error_code
error_message
retryable

evidence_ids

source_system
query_id

time_range

started_at
finished_at
```

语义固定：

```text
tool successful + empty result
→ no_data

binary missing
→ unavailable

backend unreachable
→ unavailable

authorization reject
→ permission_denied

deadline exceeded
→ timeout

backend query error
→ failed
```

禁止：

```text
permission_denied → no_data
no_data → healthy
unavailable → healthy
```

---

# 三十三、Evidence 模型

Evidence 不可变。

字段：

```text
evidence_id
run_id

tenant_id
cluster_id

evidence_type
claim_type

source
source_reliability

resource_id
namespace
service
pod
node
trace_id

observed_at
time_range_start
time_range_end

fact

raw_ref
raw_digest_sha256

metadata
provenance_fingerprint

created_at
```

大型 raw payload：

```text
MinIO
```

MySQL 只保存：

```text
raw_ref
digest
summary
metadata
```

不得把大型 raw logs/traces 整体塞 `ai_evidence`。

---

# 三十四、Evidence 类型

固定：

```text
metric_anomaly
log_pattern
log_error
trace_anomaly
k8s_state
k8s_event
alert
change
knowledge_case
topology_relation
resource_state
capacity_anomaly
hardware_event
```

claim_type：

```text
fact
inference
knowledge
unknown
```

规则：

```text
fact
→ 必须引用现场数据

inference
→ 必须引用 supporting evidence IDs

knowledge
→ 必须引用 document/source

unknown
→ 必须记录缺失 Evidence / Capability / Permission / Availability 原因
```

Hypothesis 是独立实体，不是 claim_type。

---

# 三十五、Evidence 去重

必须计算：

```text
provenance_fingerprint
```

最少基于：

```text
source
source_record_id or query_id
resource_id
observation/time range
raw digest
```

同一事实：

```text
被多个 Agent 引用
```

不得在 RCA 中重复计分。

Agent 可以引用同一 evidence_id，但不得复制成多个"新证据"提高置信度。

---

# 三十六、Source Reliability V1

固定：

```text
Kubernetes API current state     0.95
Metric / SLI                     0.95
Trace / Span                     0.90
Kubernetes Event                 0.90
Structured Change Record         0.90
Resource Graph deterministic     0.85
DeepFlow observation             0.85
Raw Log                          0.85
Log Pattern                      0.80
K8sGPT Diagnosis                 0.70
Runbook / SOP                    0.65
Historical Case                  0.60
LLM inference                    NOT Evidence
```

这些数值是 V1 固定配置。

DeepSeek 不得为了让测试通过而调高。

---

# 三十七、RCA 评分算法

基础：

```text
base_score =
    llm_reasoning_prior × 0.35
  + evidence_support    × 0.30
  + source_reliability × 0.20
  + temporal_relation  × 0.15
```

Penalty：

```text
critical contradiction
= -0.25 each
cap = -0.50

normal contradiction
= -0.10 each
cap = -0.30

missing critical evidence category
= -0.20 each
cap = -0.40
```

最终：

```text
final_score = clamp(base_score - penalties, 0, 1)
```

只要存在 unresolved critical contradiction：

```text
不得 confirmed
```

无论 score 多高。

---

# 三十八、Evidence Support 计算

Evidence 先按 provenance 去重。

Relation：

```text
direct_support   = 1.0
indirect_support = 0.6
```

使用最多：

```text
top 5 unique supporting evidence
```

计算：

```text
evidence_support =
Σ(source_reliability × relation_weight)
/
Σ(source_reliability)
```

Source Reliability component：

```text
= unique supporting evidence reliability arithmetic mean
```

禁止用大量低价值证据堆分。

---

# 三十九、Temporal Relation

固定：

```text
1.00
原因明显先于 First Bad Event，且位于同一资源或直接依赖链

0.70
原因先于异常，并位于合理相关时间窗

0.40
时间关系弱、只能证明相关

0.00
无时间支持，或候选事件发生在异常之后
```

时钟严重偏差：

```text
temporal evidence = partial/unknown
```

不得强行给 1.0。

---

# 四十、Hypothesis 判定

confirmed：

```text
final_score >= 0.80
AND >=1 direct evidence reliability >=0.85
AND no unresolved critical contradiction
```

supported：

```text
0.60 <= final_score < 0.80
```

unknown：

```text
final_score < 0.60
OR critical evidence missing
```

rejected：

```text
contradiction clearly outweighs support
```

任何：

```text
final_score < 0.60
```

不得自动 remediation。

---

# 四十一、Planner 硬预算

```text
max_initial_steps    = 12
max_followup_rounds  = 2
max_total_steps      = 20

default_tool_timeout = 30s
long_query_timeout   = 60s
llm_timeout          = 90s
run_timeout          = 900s

llm_retry            = 2
readonly_tool_retry  = 1
write_action_retry   = 0
```

达到预算仍缺证据：

```text
unknown
insufficient_evidence
```

禁止无限补查。

---

# 四十二、七类 Domain Agent

## 42.1 Observability Agent

负责：

```text
RED
USE
QPS
error rate
latency
CPU
memory
SLI
SLO
anomaly window
baseline comparison
```

输出：

```text
ToolResult[]
Evidence[]
MissingEvidence[]
```

不得直接输出最终 Root Cause。

---

## 42.2 Log Agent

仅在用户已经人工发起的 AI 调查 Run 内自动参与适用调查；不要求用户对 Log Agent 单独点击“分析日志”，但不得脱离用户触发的 Run 在后台自行启动。

输入：

```text
tenant_id
cluster_id
resource_id/service
current_window
```

Baseline：

```text
与 current_window 等长
紧邻 current_window 之前
```

归一化至少处理：

```text
timestamp
UUID
IPv4/IPv6
hex ID
large numeric ID
request ID
trace/span ID
pod dynamic suffix
extra whitespace
```

Pattern ID：

```text
SHA256(normalized_template)
```

输出：

```text
pattern_id
template
current_count
baseline_count
growth_ratio
first_seen
last_seen
severity
services
pods
trace_ids
samples
```

growth：

```text
(current_count + 1) / (baseline_count + 1)
```

异常：

```text
baseline_count=0 AND current_count>=3
→ new pattern

growth_ratio>=3
→ growing pattern

error/fatal
→ higher priority
```

V1 禁止为了日志模式先引入重型 ML/向量模型。

---

## 42.3 Trace Agent

负责：

```text
error traces
slow traces
critical span
downstream anomaly
normal vs abnormal trace comparison
```

必须输出结构化差异。

例如：

```text
orders → redis

baseline_p95=13ms
abnormal_duration=3180ms
```

---

## 42.4 Kubernetes Agent

覆盖：

```text
Deployment
StatefulSet
DaemonSet
Pod
Container
Node
Event
Probe
Scheduling
PVC
resource request
resource limit
restart
OOMKilled
CrashLoopBackOff
NodePressure
DeploymentUnavailable
```

K8sGPT 只是 Tool。

Agent 不持 Kubernetes Client。

---

## 42.5 Change Agent

至少分析：

```text
Deployment revision
image change
ConfigMap change
Kubernetes Event
platform action/workflow
```

必须参与统一 Timeline 与 First Bad Event。

---

## 42.6 Knowledge Agent

搜索：

```text
Runbook
SOP
Historical Case
RCA
Architecture Docs
Product Docs
```

默认：

```text
top_k=5
```

结果保留：

```text
document_id
source
version
similarity
applicability
```

Knowledge 不得覆盖 live fact。

---

## 42.7 Infrastructure Agent

只分析真实存在的数据：

```text
Node
Host
VM
IPMI/BMC/sensor
```

没有数据：

```text
unknown
```

禁止生成模拟硬件健康结论。

---

# 四十三、Resource Graph

V1 不引入 Neo4j。

Node：

```text
Cluster
Namespace
Node
Deployment
Pod
Service
```

Relation：

```text
runs_on
managed_by
belongs_to
calls
depends_on
```

固定能力：

```text
get_upstream
get_downstream
get_runtime_resources
get_owner
get_dependencies
```

跨 Cluster Edge：

```text
默认禁止
```

所有遍历必须重新应用：

```text
tenant
cluster
resource scope
```

过滤。

禁止 Graph 侧漏未授权资源。

---

# 四十四、First Bad Event

统一 Timeline 必须接入：

```text
change
kubernetes event
log pattern
trace anomaly
metric anomaly
alert
execution event
```

所有时间转换 UTC。

First Bad Event 必须输出：

```text
event_type
timestamp
resource_id
cluster_id
evidence_id
```

Alert 本身不得默认当根因。

---

# 四十五、OpsAction

V1 结构化动作：

```text
restart_workload
scale_workload
rollback_deployment
patch_resource
```

特殊：

```text
restricted_shell
```

OpsAction 最低字段：

```text
action_id
run_id

tenant_id
cluster_id
target_resource_id
resource_version

action_type
parameters

proposed_risk
authoritative_risk

expected_effect
verification_policy_id

rollback_strategy

action_hash
idempotency_key

created_by
created_at
```

LLM/orchestrator 只能提供：

```text
proposed_risk
```

最终：

```text
authoritative_risk
```

必须由 query-api Execution Policy Engine 计算。

不得信任 LLM 风险等级。

---

# 四十六、Action Risk

基准：

```text
restart_workload
→ R2

scale_workload
small scale-up → R2
scale-down → R3
large scale-up → R3

rollback_deployment
→ R3

patch_resource
→ R3

restricted_shell
→ R4
```

最终 Risk 还要考虑：

```text
RCA confidence
blast radius
environment
resource type
parameter delta
```

风险只能：

```text
same or higher than baseline
```

不得被 LLM 降级。

---

# 四十七、R0–R4 行为

```text
R0
read-only observability get/list
自动

R1
diagnostic / describe / top / K8sGPT
自动 + audit

R2
restart / small safe scale-up
用户本人显式 confirmation

R3
configuration/resource/deployment modification
独立 approver approval

R4
destructive/storage/network/restricted shell
严格审批或默认阻止
```

---

# 四十八、Confirmation 与 Approval 分开

R2：

```text
Confirmation
```

可以由 action requester 本人确认。

R3/R4：

```text
Approval
```

默认禁止：

```text
requester == approver
```

管理员也不能自审批。

V1 不实现一般性 Break-glass。

如果未来增加 Break-glass：

必须另做 contract、权限、审计。

---

# 四十九、Approval

字段：

```text
approval_id
run_id
action_id
action_hash

tenant_id
cluster_id
target_resource_id
resource_version

risk_level

requested_by
approved_by

created_at
expires_at

decision
```

执行前必须重新检查：

```text
approval valid
action hash unchanged
cluster unchanged
target unchanged
resourceVersion unchanged
approval not expired
current user permissions valid
current cluster authorization valid
```

任一变化：

```text
拒绝执行
```

---

# 五十、Patch 限制

`patch_resource` 禁止 arbitrary JSON Patch。

必须：

```text
resource type allowlist
field allowlist
parameter validation
```

例如允许字段由代码维护。

LLM 不得提交任意 Kubernetes 对象覆盖。

---

# 五十一、restricted_shell

仅：

```text
R4 emergency capability
```

固定：

```text
planner_selectable=false
automatic=false
```

必须：

```text
人工显式触发
严格审批
command policy
timeout
output limit
audit
credential protection
path protection
```

禁止：

```text
structured action failure → fallback shell
Agent 自动选 shell
LLM raw shell → directly execute
```

---

# 五十二、Verification

所有写 Action：

```text
Before Snapshot
→ Execute
→ Observation Window
→ After Snapshot
→ Compare
→ Verdict
```

默认 Observation Window：

```text
restart_workload      120s
scale_workload        180s
rollback_deployment   300s
patch_resource        300s
```

配置可版本化调整，但 DeepSeek 不得临时修改默认值让测试通过。

restart 至少验证：

```text
Ready replicas
Pod readiness
restart count
error rate
P95/P99
```

scale：

```text
desired replicas
available replicas
error rate
latency
saturation
```

rollback：

```text
deployment revision
ready replicas
error rate
latency
critical alerts
```

patch：

```text
target field
resource readiness
error rate
latency
health condition
```

exit code 0：

```text
不等于 success
```

---

# 五十三、Rollback

Rollback 本身必须是新的 OpsAction。

流程：

```text
new action_id
new resourceVersion
new action_hash
new authoritative risk
new approval if R3/R4
new execution
new verification
```

禁止自动执行高级别 rollback。

---

# 五十四、SSE 事件

固定：

```text
run_start
intent
plan_start
plan_step_start
plan_step_end
tool_start
tool_end
evidence
hypothesis
root_cause
remediation
confirmation_required
approval_required
execution_start
execution_end
verification
report
heartbeat
error
done
```

Envelope：

```text
run_id
sequence
event_type
timestamp

tenant_id
cluster_id nullable

payload
```

对于 multi-cluster aggregate event：

```text
cluster_id=NULL
```

payload 必须携带明确 cluster_scope/aggregate semantics。

---

# 五十五、SSE 所有权

orchestrator：

```text
Run State Machine Owner
SSE Event Semantic Owner
sequence allocator
```

query-api：

```text
Persistence Owner
Authorization Boundary
Public SSE Proxy
Replay Reader
```

浏览器只能连 query-api。

不得直连 orchestrator。

---

# 五十六、SSE Persistence 与 Replay

除 heartbeat 外的业务事件：

```text
必须持久化到 ai_run_events
```

字段至少：

```text
run_id
sequence
event_type
tenant_id
cluster_id
payload
created_at
```

唯一：

```text
(run_id, sequence)
```

Heartbeat：

```text
10–15s
不要求持久化
```

客户端断线：

```text
Last-Event-ID
or
after_sequence
```

恢复。

query-api Replay 前必须重新鉴权。

猜到 run_id 不代表可以读取。

---

# 五十七、Run Cancel

Browser：

```text
→ query-api authorization
→ RunControlContext(operation=cancel)
→ orchestrator
→ state transition
→ query-api persistence
```

SSE 断线绝不能自动 Cancel Run。

---

# 五十八、统一错误代码

至少：

```text
AUTH_REQUIRED
SESSION_REVOKED
SERVICE_AUTH_FAILED

INVALID_CONTEXT
CONTEXT_EXPIRED
CONTEXT_REPLAYED
CONTEXT_SCOPE_MISMATCH

TENANT_ACCESS_DENIED
CLUSTER_ACCESS_DENIED

RESOURCE_NOT_FOUND
RESOURCE_AMBIGUOUS
CLUSTER_UNAVAILABLE

NO_DATA
BACKEND_UNAVAILABLE

TOOL_UNAVAILABLE
TOOL_TIMEOUT

VALIDATION_FAILED

RUN_STATE_CONFLICT
RUN_CANCELLED

ACTION_NOT_ALLOWED
ACTION_CONFIRMATION_REQUIRED
ACTION_APPROVAL_REQUIRED

APPROVAL_EXPIRED
APPROVAL_SCOPE_MISMATCH

RESOURCE_VERSION_CONFLICT

MAINTENANCE_MODE
```

HTTP：

```text
401
authentication/service/context identity failure

403
authenticated but authorization denied

404
resource not found

409
state/resourceVersion/replay/ambiguity conflict

422
structured validation failure

503
backend unavailable/cluster unavailable/maintenance

504
timeout
```

`no_data`：

```text
HTTP 200
semantic status=no_data
```

---

# 五十九、Canonical API

浏览器 Public API：

```text
POST /api/v1/investigations
GET  /api/v1/investigations/{run_id}
POST /api/v1/investigations/{run_id}/cancel
GET  /api/v1/investigations/{run_id}/events

GET  /api/v1/resources/resolve
GET  /api/v1/clusters

GET  /api/v1/actions/{action_id}
POST /api/v1/actions/{action_id}/confirm
POST /api/v1/actions/{action_id}/approve
POST /api/v1/actions/{action_id}/reject
```

query-api → orchestrator：

```text
POST /internal/v1/run-invocations
POST /internal/v1/run-controls
```

orchestrator → query-api：

```text
POST /internal/v1/query/metrics
POST /internal/v1/query/logs
POST /internal/v1/query/traces
POST /internal/v1/query/alerts
POST /internal/v1/query/topology
POST /internal/v1/query/kubernetes
POST /internal/v1/query/changes
POST /internal/v1/query/knowledge
```

Control Plane：

```text
/internal/v1/control-plane/runs
/internal/v1/control-plane/run-clusters
/internal/v1/control-plane/plan-steps
/internal/v1/control-plane/tool-runs
/internal/v1/control-plane/evidence
/internal/v1/control-plane/hypotheses
/internal/v1/control-plane/actions
/internal/v1/control-plane/verifications
/internal/v1/control-plane/approvals
/internal/v1/control-plane/events
```

Execution：

```text
POST /internal/v1/execution/actions/{action_id}/precheck
POST /internal/v1/execution/actions/{action_id}/execute
POST /internal/v1/execution/actions/{action_id}/verify
```

---

# 六十、前端最终 IA

一级导航固定：

```text
总览
智能运维
可观测
资源
治理
系统管理
```

智能运维：

```text
智能调查
告警与事件
审批任务
```

可观测：

```text
服务
链路
日志与指标
```

资源：

```text
Kubernetes
主机与虚机
容量与硬件
```

治理：

```text
知识与 Runbook
变更与审计
SLO
```

系统管理：

```text
用户与权限
设置
高级
```

普通用户一级入口禁止：

```text
Tool Registry
K8sGPT
RAG
Agent
Workflow Designer
Graph Engine
Prompt
Model Provider
```

---

# 六十一、智能调查页面

必须展示：

```text
Investigation Scope
Tenant
Cluster
Resource
Time Range

Intent

Plan DAG
Step Status

Tool Runs
ToolResult
Duration

Evidence
Raw Data Link

Hypotheses
Supporting Evidence
Contradicting Evidence
Missing Evidence

Follow-up Investigation

Root Cause
Confidence Breakdown
Unknowns

Structured Remediation
Risk
Confirmation / Approval

Execution
Verification

Timeline
Audit

Detailed LLM Analysis
```

完整 LLM Markdown：

```text
只能放"详细分析"
```

不能成为主体聊天气泡。

---

# 六十二、专业页面联动

以下页面必须支持：

```text
交给 AI 调查
```

至少：

```text
Service
Logs
Trace
Kubernetes
Alerts
```

Deep link 必须携带：

```text
tenant
canonical cluster
resource_id
time range
```

服务端重新鉴权。

全局 Cluster selector 不得覆盖 Run/Evidence 原始 Cluster。

---

# 六十三、日志页面

必须有：

```text
原始日志
异常模式
```

异常模式显示：

```text
template
baseline count
current count
growth
first seen
last seen
severity
service
pod
trace correlation
```

可跳：

```text
raw logs
trace
pod
service
investigation
```

---

# 六十四、Run URL 分享

V1：

```text
允许复制含 run_id 的 URL
```

但：

```text
打开时重新 RBAC
```

仅授权用户可看。

禁止：

```text
匿名公开分享
public signed share URL
cross-tenant share
```

---

# 六十五、Workflow 最终范围

保留内部：

```text
Approval orchestration
Execution orchestration
Verification orchestration
Run recovery
necessary background jobs
```

删除普通用户：

```text
Workflow Designer
Agent Workflow
Graph Workflow Debug
```

管理员可在：

```text
系统管理 → 高级
```

查看诊断详情。

---

# 六十六、Knowledge 正式写入边界

本 Phase 重构**不新增自动知识学习子系统**。

现有 Knowledge 能力继续保留：

```text
ChromaDB = Knowledge Vector Index
MinIO-compatible Object Store = Knowledge Object SoT
Knowledge Agent = 只读检索/辅助调查
```

禁止：

```text
Run/RCA 完成 → 自动写正式知识库
LLM 推理 → 自动变成 Knowledge fact
未经审核的处置经验 → 自动成为后续 RCA 权威依据
```

若业务后续需要把成功调查沉淀为案例，只允许走现有人工 Review 流程；Review Approved 后才能进入正式 Knowledge。该能力不是当前重构 DoD，不得为此新增 `Learning Engine`、`Incident Learning` 状态机或平行知识存储。

---

# 六十六-A、V9.3 平台定位与最小能力边界

本节为 `V9.3_FROZEN`。它只明确产品边界，不改变 V9.2 R2 的唯一生产主链。

最终产品定位固定为：

> **面向云原生与边缘基础设施的全栈智能运维分析与处置平台。**  
> 平台作为统一智能运维处理中心，统一接入平台自身及已注册的其他 Kubernetes 平台及其关联基础设施的多源运行数据。告警、事件、变更和运行事实可以自动采集、存储、展示，但**所有 AI 分析必须由已认证用户人工显式触发**。用户发起 AI 调查后，由 AI Agent 在该 Run 内自动完成跨指标、日志、链路、Kubernetes、基础设施及变更的证据收集与关联分析，定位故障根因，生成安全、可审计的处置方案，并通过授权执行与效果验证形成“人工发起调查—定位根因—安全处置—验证恢复”的智能运维闭环。

这里的“发现问题/调查入口”固定解释为：

```text
Alert / Event / Change / 异常事实
→ MAY be automatically collected and displayed
→ MUST NOT automatically create/start AI analysis Run

Authenticated user
→ explicitly clicks/submits AI investigation / alert analysis / root-cause analysis
→ create Investigation Run
```

当前版本**不建设独立 Detection Engine，不主动扫描/发现外部 Kubernetes 平台，不自动获取外部凭据，不建立 Event Center 或 Incident Engine，也不允许后台/定时/SystemPrincipal 自动触发 AI 调查**。

### 66-A.1 AI 调用人工触发边界（V9.3_FROZEN）

所有会进入 AI 推理链的入口统一遵守：

```text
root-cause analysis
alert analysis
AI investigation
AI diagnosis
AI Chat compatibility entry（若过渡期仍存在）
Planner/Agent investigation
K8sGPT AI diagnosis
LLM analysis

NEW independent AI analysis
→ MUST originate from explicit authenticated human action
→ MUST bind to user principal + valid session + authorized tenant/cluster/resource scope
→ MUST create/use a user-triggered Investigation Run
```

禁止：

```text
Alert received → automatically start AI analysis
Event received → automatically start AI analysis
Change received → automatically start AI analysis
cron/scheduler → automatically start AI analysis
background worker → automatically start AI analysis
SystemPrincipal → create/start AI analysis Run
automatic retry after a Run has ended/cancelled → start a new AI Run
```

允许：

```text
telemetry / alert / event / change automatic ingestion
non-AI storage / indexing / readiness / health processing
UI automatically showing existing alerts/events/changes

after ONE explicit human AI trigger:
Run → Intent → Planner → Domain Agents → Tool/LLM/K8sGPT calls
may proceed automatically inside that same authorized Run
without requiring a separate human click for every Agent/LLM sub-call
```

本规则不替代写动作安全门。即使 AI Run 已由用户人工触发，R2 confirmation、R3/R4 independent approval、authoritative risk、Execution Verification 仍按既有合同执行。

最终主链固定为：

```text
Alert / Event / Change / Resource Context
→ shown to user / selected by user
→ Explicit Authenticated Human AI Trigger
→ Investigation Run
→ Intent
→ Planner
→ Investigation DAG
→ Domain Agents
→ Tool Registry
→ Trusted Query Boundary
→ ToolResult
→ Evidence
→ Hypothesis RCA
→ Missing Evidence / Follow-up
→ Root Cause / Unknown
→ Structured OpsAction
→ Authoritative Risk
→ Confirmation / Approval
→ Execution
→ Verification
→ Run Completion
```

已有模型职责保持：

```text
Run                 = 一次完整智能运维调查/处置/验证过程
Run Event           = Run 内结构化事件历史
Alert/Event/Change  = 调查事实输入
Timeline            = 相关事实的时间关联结果
Knowledge           = 已审核知识，只读辅助 RCA
Multi-Cluster       = 多 Kubernetes/基础设施来源的隔离与调查范围
```

不得因为“平台要完整”而新增：

```text
第二套 Incident 生命周期
第二套 Detection/Anomaly 平台
第二套 Event Center
边缘自治控制面
大规模边缘节点生命周期治理
自动 Learning Engine
operation_level 授权模型
第二套 CMDB
第二套 Kubernetes 控制面
```

---

# 六十六-B、数据源注册与统一接入边界

本节为 `V9.3_FROZEN`，**仅从 V9.3 激活（Phase 7）开始生效**。这是 V9.3 相对 V9.2 唯一新增的业务边界补充，且必须最小化实现；不得据此回改、补测或重开 V9.2 Phase 1–6。

## 66-B.1 数据来源

平台处理的数据来源只分两类：

```text
1. 平台自身运行数据
2. 已注册的外部 Kubernetes 平台及其关联基础设施运行数据
```

两类数据必须进入同一套：

```text
Tenant / canonical Cluster / Resource Identity
→ Unified Query Layer
→ Tool Registry
→ ToolResult
→ Evidence
→ Hypothesis RCA
```

禁止因为 `internal/external` 再建立两套 Query、Tool、Evidence 或 RCA 主链。

## 66-B.2 注册优先复用现有模型

外部 Kubernetes/基础设施数据源注册优先复用：

```text
Cluster Registry
tenant_clusters
credential_ref
platform/data source config
Kubernetes Access Boundary
现有 VM/VLogs/ClickHouse/OTLP/DeepFlow 等接入配置
```

若真实代码已有足够字段，禁止新建 `data_sources` 平行表、独立注册微服务或新的身份体系。

只有当真实代码无法表达一个强制接入字段时，才允许在当前权威模型上做最小扩展；涉及新表、新 SoT 或新身份权威时必须先走 Architecture Deviation，不得自行批准。

逻辑上注册信息至少能够回答：

```text
source identity/reference
owning tenant
canonical cluster when source is cluster-bound
endpoint/config reference
credential_ref where required
available capabilities: metrics/logs/traces/kubernetes/infrastructure/alerts/changes
current connectivity/readiness
```

这些是逻辑要求，不强制新增一张新表。

## 66-B.3 禁止主动发现外部平台

禁止：

```text
扫描网段发现 Kubernetes API
自动枚举未注册 Cluster
自动抓取/猜测 kubeconfig 或 token
未注册 source 自动进入生产事实查询
按 endpoint/context/node name 代替 canonical cluster identity
```

允许：

```text
管理员/受控流程完成注册
注册后验证 endpoint/credential/identity
注册后探测该 source 实际支持的数据能力
注册后持续做 connectivity/readiness check
```

“能力探测”只针对**已注册 source**，不是平台发现。

## 66-B.4 平台自身也是被运维对象

五个自研服务及其依赖的运行事实必须允许进入现有统一调查体系：

```text
observability-frontend
ai-apm-query-go
ai-apm-ingest-go
ai-event-collector
ai-orchestrator
MySQL / ClickHouse / VictoriaMetrics / VictoriaLogs / Chroma / MinIO
其所在 Kubernetes runtime
```

必须能区分：

```text
业务对象真的 no_data
数据源 unavailable
平台 ingest/query 链路故障
permission_denied
timeout
```

禁止因为“查不到外部数据”直接判断被运维对象健康或无异常。

## 66-B.5 Provenance 与 scope

无论数据来自平台自身还是外部 source，Evidence 都必须保留现有 provenance，并至少能够回溯：

```text
source/provider
observed time
query/tool run
canonical tenant/cluster/resource scope where applicable
```

数据源注册不创造新的授权维度：

```text
source_id != tenant authority
source_id != cluster authority
source_type != capability authorization
```

授权仍只服从 V9.2 R2 已冻结的 MySQL current authorization、canonical Cluster、Resource Identity、TrustedRequestContext 和 capability。

---

# 六十六-C、DeepSeek 强约束执行协议

本节为 `V9.3_FROZEN`，直接约束 DeepSeek 的执行行为。

## 66-C.1 DeepSeek 的角色

V9.2 Gate 6 PASS、V9.3 激活之后：

```text
DeepSeek = constrained implementer / verifier
DeepSeek != architecture decision maker
```

不得因为“更简单”“更常见”“更符合个人偏好”改掉冻结架构。

## 66-C.2 每个 Task 开始前必须先输出内部执行卡

至少记录：

```text
CURRENT_PHASE
CURRENT_TASK
CONTRACT_CLAUSES
CURRENT_CODE_FACTS
FILES_EXPECTED_TO_CHANGE
FILES_MUST_NOT_CHANGE
TEST_FIRST_PLAN
RUNTIME_VALIDATION_PLAN
DATA_MUTATION_PLAN
SECURITY_RISK
GIT_ACTION=NONE
```

若实际代码事实与预期不同，先更新 Code Map，再继续；只有真实架构冲突才 BLOCKED。

## 66-C.3 每个功能修改必须 TDD

严格执行：

```text
read current implementation
→ add failing test
→ prove intended failure
→ minimum implementation
→ focused test
→ adjacent test
→ race/static check where applicable
→ remove obsolete path
→ retest
```

禁止“先批量改 20 个文件，最后补几个测试”。

## 66-C.4 不允许因上下文长度暂停

以下都不是 BLOCKER：

```text
上下文太长
剩余工作多
单个 Phase 很大
需要多个测试容器
需要分批汇报
```

可在同一 Phase 内分 checkpoint 汇报，但必须继续执行已授权、非破坏性的当前任务。

只有以下情况允许停下来询问：

```text
真实架构冲突且合同无法按现状实现
需要用户批准 Architecture Deviation
需要 Phase 17 或其他明确破坏性授权
真实必需环境不可获得，导致 Gate 无法满足
需要用户提供无法从项目/环境解析的外部凭据或业务决定
```

## 66-C.5 禁止自行批准偏差

发生差异时必须输出：

```text
ACTUAL_CODE_FACT
CONTRACT_REQUIREMENT
WHY_CONFLICTS
MINIMUM_DEVIATION
SECURITY_IMPACT
DATA_IMPACT
TEST_IMPACT
USER_DECISION_REQUIRED
```

在用户批准前不得偷偷实现 deviation。

## 66-C.6 每个 Task 完成后必须输出证据卡

```text
TASK
STATUS
IMPLEMENTED
FILES_ADDED
FILES_MODIFIED
FILES_DELETED
TESTS(command/exit/result)
REAL_RUNTIME_VALIDATION
SECURITY_NEGATIVE_TESTS
DATA_MUTATION
KNOWN_LIMITATIONS
DEVIATIONS
GIT_ACTION=NONE
NEXT_TASK
```

## 66-C.7 Phase Gate 证据要求

Gate PASS 必须基于：

```text
code evidence
automated tests
negative tests
real runtime evidence where contract requires
exact command + exit code
```

禁止：

```text
“代码看起来支持” → PASS
“mock 通过” → real backend PASS
“contract 已冻结” → runtime bootstrap PASS
“日志无报错” → verification recovered
```

---

# 六十六-D、所有 Phase 的统一执行模板

从 **Phase 7 到 Phase 21**，每个 Phase 必须同时遵守本模板与该 Phase 的专属细化任务。Phase 1–6 不适用本模板，只服从 V9.2 FINAL R2。

## Entry Criteria

进入前必须：

```text
previous Gate PASS
NEXT_PHASE 已由上个 Phase 标记为 NOT_STARTED
required documents available
required test/runtime dependencies classified AVAILABLE/UNAVAILABLE/UNKNOWN
no unresolved contract deviation that affects this Phase
```

## Mandatory Task Pattern

每个 Phase 至少包含：

```text
Pn.1 Inventory / Baseline
Pn.2 Contract / Ownership confirmation
Pn.3 Implementation
Pn.4 Integration
Pn.5 Negative & security tests
Pn.6 Real-runtime validation where applicable
Pn.7 Obsolete-path cleanup
Pn.8 Automated Gate evidence
Pn.9 Documentation / report update
```

允许具体 Phase 调整编号，但不得缺失相应责任。

## Test Matrix

适用时至少覆盖：

```text
positive
negative
boundary
permission
timeout
backend unavailable
no_data
idempotency
replay
restart/recovery
race/concurrency
cross-tenant
cross-cluster
tampering
real backend
```

## BLOCKED 条件

只有无法满足强制 Gate 的真实外部条件才能 BLOCKED。BLOCKED 必须列出：

```text
blocking fact
required contract
attempted evidence
why not satisfiable now
minimum next action
```

## Exit State

每个 Phase 结束必须回到：

```text
PHASE: N
STATUS: PASS / FAIL / BLOCKED
GIT_ACTION: NONE
NEXT_PHASE: NOT_STARTED
```

`STATUS != PASS` 时禁止进入下一 Phase。

---

# 六十七至七十二、Phase 1–6：V9.2 FINAL R2 独占执行区

## V9.3 不定义 Phase 1–6 执行任务

Phase 1–6 的目标、任务、Gate、测试、偏差与最终 PASS 结论全部以：

```text
aiops-agentic-v9.2-final-r2.md
```

为唯一权威。

V9.3 对 Phase 1–6：

```text
NO new task tree
NO additional Gate assertion
NO retrospective test requirement
NO state reinterpretation
NO schema/auth/query/writer/reader backport requirement
NO reopen after PASS
```

V9.3 只消费 `V9.2_BASELINE_AFTER_GATE6` 作为 Phase 7 的输入。

如果后续 Phase 7+ 为新增能力修改了曾在 Phase 1–6 建立的代码或配置：

```text
that change belongs to the current Phase 7+
!= reopening Phase 1–6
!= changing V9.2 Gate history
```

并且必须证明不会退化 V9.2 Gate 6 及其前置 Gate 的不变量。

---

# 七十三、Phase 7：Tool Registry、Evidence、Intent、Planner

## V9.3 Phase 7 详细执行任务树

**目标：从“统一事实查询平台”升级为受控 Agentic 调查内核。继续复用现有 Run 作为一次调查的唯一业务主对象，不新增 Incident/Detection 主链。**

### Entry Criteria

```text
V9.3_STATUS = ACTIVE
V9.3 Activation Record exists
V9.2 Gate 6 PASS
new writer/reader active
legacy writer/reader/fallback absent
canonical internal query API ready
```

不得为了 Phase 7 需求回开 Phase 6；如需扩展已有 query-api/orchestrator，只能作为 P7 增量实现，并持续满足 Gate 6 不变量。

### P7.1 Tool Registry

`ToolDefinition` 至少：tool_id/version/domain、input/output schema、read_only、baseline_risk、required_capability、allowed_scope、timeout/retry、backend。未注册 Tool 不可执行；Agent 不得自带隐藏 Tool。

### P7.2 InternalQueryClient + TrustedContextIssuer

orchestrator 只能通过 `/internal/v1/query/*` 获取事实。每次调用按 tenant/cluster/capability 签发 TrustedRequestContext；禁止 direct DB/CH/VM/VLogs/K8s。

### P7.3 ToolResult Normalization

所有 Tool 精确映射：success/partial/no_data/failed/timeout/unavailable/permission_denied。backend 403 不得变 no_data；network error 不得变 no_data。

### P7.4 Evidence Hub

ToolResult→Evidence：normalize→provenance key/hash→dedup→reliability→immutable metadata→必要时 MinIO object ref。LLM inference 绝不作为 Evidence。

### P7.5 Intent Engine

输出 intent/action_mode/target_type/resource/cluster scope/time range/symptom/ambiguity；歧义→`RESOURCE_AMBIGUOUS`，禁止猜。

### P7.6 Planner

Planner 是唯一调查 DAG 控制器。实现预算计数、step dependency、parallel-safe branches、MissingEvidence follow-up slots；严格执行冻结预算和 timeout/retry。

### P7.7 Structured Investigation State

只围绕现有 Run 维护：

```text
pending/running/completed steps
ToolResult refs
Evidence refs
missing evidence
budget consumed/remaining
```

禁止仅靠 prompt 历史恢复状态，也禁止再新增 Incident/Detection 状态机。

### P7.8 Manual AI Invocation Boundary

AI 调查入口统一收敛为**人工显式触发**。Alert/Event/Change 只能作为页面上下文、预填 scope/time/resource/symptom 和 Evidence 查询起点，不能自行创建 Run。

固定：

```text
Alert/Event/Change exists
→ no AI Run automatically created
→ no Planner/Agent/LLM/K8sGPT automatically invoked

Authenticated user explicitly invokes AI analysis
→ realtime authorization
→ RunInvocation
→ Intent
→ Planner
```

同一个用户手动发起的 Run 内，Planner/Agent 可以按 DAG 自动继续执行，不要求每个 Agent 或每次 LLM 子调用再次人工点击。Run 完成、取消或失败后，后台不得自动创建新的 AI Run。不得新增独立 Detection Engine 或告警自动 Agent 图。

### P7.9 Registered Data Source Mapping（最小化、条件式）

本任务不创建新的 source 身份或授权体系。先确认平台自身数据与外部 Kubernetes 平台是否已经能够通过 V9.2 建立的 `Cluster Registry / tenant_clusters / credential_ref / platform-data-source config / canonical cluster` 映射进入统一 Query。

规则：

```text
existing model sufficient
→ reuse only
→ no new table/service/error enum

missing one mandatory registration field
→ minimal extension in existing authoritative model
→ implement as Phase 7 change
→ preserve Gate 6 query semantics

requires new SoT / new identity authority / second query path
→ BLOCKED + Architecture Deviation
```

平台自身数据与已注册外部平台必须使用相同 ToolResult 状态和 Evidence provenance。外部平台不可达仍使用现有 `unavailable` 语义；未知/未注册 Cluster 或无有效配置必须由现有 cluster/config fail-closed 语义处理，不为此新增一套 `source authorization`。

### P7.10 Security / Negative Tests

至少：unregistered Tool、LLM 修改 risk/read_only、wrong capability、cross-cluster Tool、budget exceeded、ambiguous target、backend unavailable、permission denied、unknown/unregistered canonical cluster、registered mapping points to wrong canonical cluster。

### Gate 7 追加断言

```text
Tool Registry unique production entry
InternalQueryClient only fact path
all AI analysis starts require explicit authenticated human trigger and converge to existing Run/Planner
platform-self and registered external cluster mappings share the same fact semantics
Evidence provenance reproducible
no Incident/Detection parallel subsystem
```



实现：

```text
Tool Registry
ToolResult
Evidence Hub
Intent Engine
Planner
Structured State
InternalQueryClient
TrustedContextIssuer
```

Planner 是唯一调查控制器。

不得存在第二套 Agent 主图决定调查顺序。

必须严格执行预算。

目标歧义：

```text
RESOURCE_AMBIGUOUS
```

证据不足：

```text
unknown
missing_evidence
```

不得强行给根因。

## Gate 7

```text
unregistered Tool cannot execute
LLM cannot change Tool risk/read_only
cross-cluster Tool call rejected
ToolResult status exact
Planner budget exact
```

Gate 后停止。

---

# 七十四、Phase 8：七类 Agent + Resource Graph

## V9.3 Phase 8 详细执行任务树

### P8.1 Agent Runtime Framework

统一执行：PlanStep→validate scope/budget→select registered Tool→Tool Registry→normalize ToolResult→Evidence Hub→MissingEvidence→return Planner。Agent 不保留第二状态机。

### P8.2 Observability Agent

实现 metrics/RED/SLI/SLO/current-vs-baseline；输出 first abnormal timestamp、delta、Evidence refs；严格区分 no_data/unavailable。

### P8.3 Log Agent

仅在**用户已手动发起的调查 Run 内**按 Planner DAG 自动参与；覆盖 raw logs、new/growing pattern、keyword/error trend、service correlation。用户不需要为 Log Agent 单独二次点击，但没有人工 AI Run 触发时 Log Agent 不得后台自行启动。

### P8.4 Trace Agent

覆盖 slow/error trace、critical span、dependency path、current/baseline comparison、trace→logs/service linkage。

### P8.5 Kubernetes Agent

覆盖 workload/pod/node/events/restarts/OOM/CrashLoop/pressure/K8sGPT。只能通过 Tool Registry→query-api Kubernetes boundary。

### P8.6 Change Agent

把 deployment/config/scale/restart/execution/resource-event 组织为 change timeline，不得默认“最近变更=根因”。

### P8.7 Knowledge Agent

Chroma 检索 + MinIO object；无结果严格 no_data。Runbook/SOP/历史案例只能作为知识证据，不能冒充 live fact；本 Phase 不增加自动 Learning。

### P8.8 Infrastructure Agent

覆盖已注册 source 能提供的 node hardware、SEL、sensor、capacity、host/infrastructure 状态。数据源未提供 sensor→unknown；数据源/后端不可达→unavailable；不得假装 healthy。

### P8.9 Resource Graph V1

构建 typed node/edge：cluster/namespace/workload/pod/service/node/dependency/change。每次 traversal 先权限过滤，默认禁止跨 Cluster edge。

### P8.10 Agent Contract Tests

每个 Agent 至少：success、no_data、permission_denied、unavailable、timeout、missing evidence、wrong cluster、budget exhaustion；平台自身 source 与已注册外部 source 至少各覆盖一个事实读取路径。

### Gate 8 追加断言

```text
all seven agents use same runtime contract
no direct DB/K8s client
Log Agent auto participation
source unavailable != no_data/healthy
Graph traversal cannot leak cross-cluster nodes
no edge autonomy/governance subsystem
```



严格按本文件实现七类 Agent。

统一 Agent I/O：

```text
INPUT:
PlanStep
Context
Existing Evidence

OUTPUT:
ToolResult[]
Evidence[]
MissingEvidence[]
```

Agent 禁止：

```text
final root cause
direct execution
direct DB
direct K8s client
```

Gate：

```text
Log Agent auto run
Trace Agent slow/error comparison
Kubernetes Agent OOM/CrashLoop
Change Agent timeline
Knowledge empty = no_data
Infra missing sensor = unknown
Graph no cross-cluster leakage
```

Gate 后停止。

---

# 七十五、Phase 9：Hypothesis RCA、补查、排序、Timeline

## V9.3 Phase 9 详细执行任务树

### P9.1 RCA Input Snapshot

每轮 RCA 基于明确 Evidence snapshot；记录 Evidence IDs、version/time，不允许 LLM 在 scoring 中偷偷引入未登记事实。

### P9.2 Hypothesis Generator

生成多个 candidate，每个包含：claim、affected resource、expected mechanism、required support、potential contradiction。禁止直接生成 confirmed root cause。

### P9.3 Support Matcher

把 Evidence→Hypothesis 的支持关系结构化，计算 evidence support；同一 provenance 重复 Evidence 不重复加权。

### P9.4 Contradiction Checker

主动搜索：时间矛盾、资源/cluster 矛盾、指标与日志/trace 矛盾、变更发生在故障后等反证。

### P9.5 Missing Evidence Engine

每条 hypothesis 明确 critical/optional missing。critical missing 会限制最终状态，不得通过语言润色掩盖。

### P9.6 Follow-up Planner

只针对 missing evidence 新增步骤，仍由唯一 Planner 控制并受全局预算。follow-up 不能开启第二调查图。

### P9.7 Fixed Scoring

严格使用冻结公式与 reliability；输出各分项和 penalty，结果可复算。禁止 LLM 直接给最终 confidence 数字覆盖公式。

### P9.8 Root Cause Ranker

排序时输出：score、support、contradictions、missing、confidence state。confirmed 必须满足原文 direct evidence/reliability/contradiction 条件。

### P9.9 Timeline / First Bad Event

统一 change/event/log-pattern/trace/metric/alert/execution 的时间轴；First Bad Event 是时间推断结果，不默认等于 Alert 或 Change。

### P9.10 Unknown-safe Behavior

无法达到阈值或 critical evidence 缺失时：

```text
root_cause = unknown
missing_evidence = explicit
no automatic remediation
```

### Gate 9 追加断言

```text
same Evidence snapshot produces reproducible score components
contradictory evidence lowers/blocks confirmation
missing critical evidence blocks automatic remediation
prompt-only RCA path absent
```


实现：

```text
Hypothesis Generator
Support Matcher
Contradiction Checker
Missing Evidence Engine
Follow-up Planner
Scorer
Root Cause Ranker
Timeline
First Bad Event
```

禁止 prompt-only RCA。

固定使用 V1 评分公式。

## Gate 9

必须自动验证完整链：

```text
Intent
Plan
Tool
ToolResult
Evidence
Hypothesis
Support
Contradiction
Missing
Follow-up
Re-score
Root Cause
Confidence
Unknowns
```

Gate 后停止。

---

# 七十六、Phase 10：Run Persistence、SSE、Recovery

## V9.3 Phase 10 详细执行任务树

### P10.1 Run State Machine + CAS

只扩展既有 Run persistence；所有状态迁移校验合法性；`state_version` optimistic CAS；冲突返回明确 409，禁止 last-write-wins。

### P10.2 Persistence Boundary

orchestrator semantic owner；query-api persistence owner；所有 V9.2 新 AI Runtime 数据经 `/internal/v1/control-plane/*`。本 Phase 不新增 Incident/Detection runtime tables。

### P10.3 Event Persistence

business SSE event 持久化到 `ai_run_events` 后才允许可靠 replay；sequence 单调，不允许多 owner 争抢 sequence。

### P10.4 Public SSE Proxy

Browser 只连 query-api；heartbeat 10–15s；disconnect 不 cancel；public reconnect 每次重新授权。

### P10.5 Replay

支持 `Last-Event-ID` / `after_sequence`；超出 retention 必须明确错误或完整状态 reload，不能 silently skip。

### P10.6 Recovery

orchestrator restart：扫描未终结 Run→恢复 runtime state→重建可继续步骤。Checkpoint 仅辅助 runtime recovery，不作为 business history。

### P10.7 Idempotency

覆盖 duplicate request_id、run creation、control command、event append、recovery re-entry。

### P10.8 Cancel

cancel 是显式 control action；SSE disconnect、browser close、timeout 都不能自动等价 cancel。

### Gate 10 追加断言

```text
Run relationships survive restart
CAS conflict deterministic
recovery does not duplicate Tool/Action
SSE replay preserves sequence and authorization
no parallel Incident persistence introduced
```



实现：

```text
Run State Machine
optimistic CAS
Run persistence
Event persistence
SSE
heartbeat
replay
recovery
cancel
```

Checkpoint：

```text
仅 runtime recovery
```

不得作为业务历史 Source of Truth。

## Gate 10

```text
orchestrator restart recovery
duplicate request_id idempotent
illegal transition 409
cancel works
SSE reconnect replay
event sequence monotonic
unauthorized replay rejected
```

Gate 后停止。

---

# 七十七、Phase 11：Remediation、Risk、Confirmation、Approval、Execution、Verification

## V9.3 Phase 11 详细执行任务树

### P11.1 Structured OpsAction Factory

只有 Hypothesis/RCA 达到允许条件后才能形成 Structured OpsAction；字段完整、single-cluster/resource scope、idempotency key、resourceVersion、expected effect、verification policy 必须齐全。

### P11.2 Execution Policy Engine

query-api 基于 action type、parameter delta、RCA confidence、blast radius、environment、resource type 计算 `authoritative_risk`，永远不能低于 baseline。继续使用既有 R0–R4 Risk，不新增 L0–L4 Autonomy 模型。

### P11.3 Confirmation

R2 requester 显式确认。确认绑定 action hash/version/target/risk/resourceVersion；修改任一字段需重新确认。

### P11.4 Approval

R3/R4 独立 approver；requester!=approver，admin 也不例外。approval 绑定 immutable action identity；cross-cluster approval 拒绝。

### P11.5 Precheck

执行前重新获取 current authorization、target identity、resourceVersion、current health、conflicting action、maintenance constraints。任一不满足不执行。

### P11.6 Execution Adapter

固定 query-api security module；只接受 structured action。不得接 raw LLM shell。每种 action 独立 allowlist/parameter validator。

### P11.7 patch_resource / restricted_shell

patch 只允许明确资源/字段；restricted_shell 仍 R4、planner_selectable=false、automatic=false，不得成为 action failure fallback。

### P11.8 Observation / Verification

严格：before snapshot→execute→observation window→after snapshot→compare→verdict。退出码只是 execution fact，不是 recovery verdict。

### P11.9 Regression Stop

`regressed` 后立即停止后续自动 action，要求人工重新调查或新 Run；禁止自动连续试错。

### P11.10 Rollback as New Action

rollback 生成新 action_id/version/hash/risk/approval/execution/verification，不允许“撤销按钮”绕过 policy。

### Gate 11 追加断言

```text
R2/R3/R4 human gates cannot be bypassed
no separate Autonomy Level changes risk semantics
Action mutation invalidates confirmation/approval
verification uses SLI/health not exit code
```



实现：

```text
Structured OpsAction
Execution Policy Engine
Confirmation
Approval
Execution Adapter
Verification
Rollback as Action
```

最终 Risk 必须 query-api 重算。

LLM Risk 只作建议。

R2：

```text
confirmation
```

R3/R4：

```text
approval
```

R3/R4 禁止自审批。

## Gate 11

```text
approval bypass impossible
action tampering invalidates approval
resourceVersion change rejects execution
exit 0 + unhealthy SLI ≠ success
regressed blocks subsequent actions
rollback creates new action
restricted_shell cannot be Planner-selected
```

Gate 后停止。

---

# 七十八、Phase 12：前端产品收敛

## V9.3 Phase 12 详细执行任务树

### P12.1 Final IA

只保留六大导航。旧 AI Chat/Tool/Workflow/Graph/Prompt/Provider 管理不得作为普通用户顶层主产品。

### P12.2 智能运维 / 调查中心

以现有 Run 为主对象展示**用户人工发起**的调查：cluster/resource/symptom/investigation status/root cause/confidence/action/verification。Alert/Event/Change 页面只能提供“AI 分析/根因分析/交给 AI 调查”等显式按钮并预填上下文；页面加载、告警到达或事件刷新不得自动创建 Run。不得为了 UI 再建立 Incident Inbox 数据模型。

### P12.3 智能调查页

完整展示 scope、Intent、Plan DAG、ToolResult、Evidence、Hypothesis、contradiction、missing evidence、root cause、unknown、action/risk/approval/execution/verification、timeline/audit。

### P12.4 Professional Page → Manual AI Investigation

Service/Logs/Trace/Kubernetes/Alerts 页面必须通过用户显式按钮触发 AI 调查，deep-link exact tenant/canonical cluster/resource/time；服务器重新鉴权。仅查看页面、切换资源、收到新告警不得产生 AI Run。

### P12.5 Logs UX

Raw Logs 与异常模式分开展示，对应 VLogs raw SoT 与 CH derived analytics；UI 不把 pattern 当 raw log。

### P12.6 Evidence Deep Link

Evidence link 固定原 Run/Evidence cluster，不受当前 global selector 覆盖；URL 打开时重新授权。

### P12.7 SSE UX

显示 reconnect/replay；permission_denied/no_data/unavailable/timeout 必须不同视觉/文案，不得统一“暂无数据”。

### P12.8 Data Source Administration Boundary

优先复用现有 Cluster/平台配置管理能力，在“系统管理”内提供或收敛最小注册入口：canonical Cluster、endpoint/config reference、credential_ref（需要时）、capability/readiness。

如果现有管理页/API 已能完成注册，只补齐必要展示/校验；如果完全没有注册入口，允许在 Phase 12 基于现有 Cluster Registry/config authority 增加最小管理 UI/API，但不得建立新身份体系、第二 CMDB、独立数据源微服务或主动发现功能。禁止新建顶层“边缘治理/节点治理”产品入口。

### Gate 12 追加旅程

```text
registered source → professional page → explicit user AI trigger → investigation → evidence → RCA
platform-self service → explicit user AI trigger → investigation → evidence → RCA
alert/event/change visible without user trigger → zero AI Run / zero LLM call
unavailable source displayed as unavailable, not no_data
```



严格采用六大导航。

AI Chat 主产品形态删除，改成：

```text
智能调查
```

专业页面加入：

```text
交给 AI 调查
```

Log Page：

```text
原始日志
异常模式
```

证据 Deep Link 固定 Run/Evidence Cluster。

Run URL 重新鉴权。

SSE reconnect 从 persisted Run 恢复。

## Gate 12

必须完成真实主旅程 UI 测试：

```text
service → investigation → evidence → professional page

alert → investigation → RCA → remediation

approval → execution → verification
```

Gate 后停止。

---

# 七十九、Phase 13：完整权限矩阵与安全加固

## V9.3 Phase 13 详细执行任务树

### P13.1 Machine-readable Authorization Matrix

维度：principal/tenant/cluster/namespace/resource/capability/action/risk/confirmation/approval。生成 fixture 供 API/Tool/Execution tests 共用；数据源注册不成为新的授权维度。

### P13.2 Public API Tamper

覆盖 localStorage role、JWT body/request tenant、cluster、resource、run/evidence/action ID guessing。前端隐藏按钮不计安全控制。

### P13.3 Internal Protocol Tamper

覆盖 wrong service token、wrong direction credential、JWS tamper、wrong kid/issuer/audience、expiry、nonce replay、scope mismatch、capability escalation。

### P13.4 Registered Source Security

覆盖 unknown/unregistered canonical cluster、credential_ref mismatch、registered endpoint/config tamper、mapping 指向错误 canonical cluster、capability/config 越界。注册映射不能绕过既有 tenant/cluster/resource authorization，也不得引入新的 source authorization 维度。

### P13.5 Manual AI Trigger Security

服务端必须证明 AI Run 创建/启动入口只接受已认证 user principal 的显式请求。至少拒绝：SystemPrincipal 自动建 Run、内部 service token 直接启动 RCA、后台 worker/cron 触发 AI、仅 Alert/Event/Change 到达即触发 AI、无有效 session 的 AI invocation。不得只依赖前端按钮隐藏。

### P13.6 Approval Security

覆盖 requester=approver、admin self-approval、cross-tenant/cluster approval、stale action hash、stale resourceVersion。

### P13.7 NetworkPolicy

按真实通信图最小化 east-west access；重点证明 orchestrator 无 target K8s credential/direct K8s egress，Agent 无 DB/storage direct access。

### P13.8 Secret Separation

验证 service credential、signing private/public key、cluster credential、LLM key 分离且只经 Secret 注入；报告只写引用和 digest/metadata。

### Gate 13

必须能用服务端测试独立证明所有越权失败；任何仅依赖 UI 隐藏的控制视为 FAIL。



建立完整 Matrix：

```text
principal
tenant
cluster
namespace
resource
capability
action
risk
confirmation/approval
```

测试：

```text
localStorage role tampering
tenant tampering
cluster tampering
internal header forgery
run ID guessing
evidence ID guessing
cross-cluster approval
admin behavior
system principal cannot start AI analysis Run
```

网络策略限制：

```text
orchestrator
query-api
DB
Kubernetes access
```

## Gate 13

服务端独立阻止所有越权。

前端隐藏按钮不算安全控制。

Gate 后停止。

---

# 八十、Phase 14：删除旧代码、接口、页面、双主路径

## V9.3 Phase 14 详细执行任务树

### P14.1 Call Graph Before Delete

对每个 legacy candidate 获取 caller/callee/route/build reference。删除前必须证明：replacement ready、production caller=0、tests covered。

### P14.2 Legacy Token Scan

搜索原文关键词外增加：

```text
legacy reader mode
ProxyAI fact fallback
old Tool Router
prompt RCA
RequestContext
HS256
current-context
cluster=*
raw log ClickHouse writer
```

### P14.3 Backend Main-path Removal

删除旧 AI Chat investigation、old Tool Router、prompt RCA、workflow investigation、old graph entry、schema adapters、legacy internal query/auth adapters。

### P14.4 Session / Checkpoint Cleanup

删除旧 business session/checkpoint history path；保留 runtime recovery checkpoint/WAL。

### P14.5 Writer/Reader Transition Cleanup

确认 Phase 6 已删：legacy ReaderMode、old raw-log writer/reader、fallback、transition flags。若仍 active，Phase14 不得掩盖，应 BLOCKED 回查 Gate6。

### P14.6 Frontend Cleanup

删除 dead page/route/API client/state/style，同时保留 Investigation/Professional page 主链。

### P14.7 Dependency Cleanup

每删依赖必须证明无 runtime import/command；不得靠删除 CA/timezone/WAL/health/recovery 减体积。

### P14.8 Quantification

输出 files/LOC/routes/handlers/pages/APIs/dependencies removed，以及保留理由清单。

### Gate 14

静态 legacy scan 结果必须逐项解释；0 production active legacy path，用户 untracked/backup/binary 未删除。


先调用图分析。

搜索：

```text
legacy
old
deprecated
compat
fallback
migration
checkpoint
session
default tenant
default cluster
X-Tenant-ID
```

删除：

```text
legacy AI Chat main path
legacy session business model
legacy checkpoint history adapter
prompt-only RCA
legacy tool router
workflow main investigation path
old graph main entry
X-Tenant-ID compatibility
old schema adapters
dead route
dead handler
dead frontend page
dead API client
dead test
dead styles/state
```

保留：

```text
WAL
recovery
command policy
K8s precheck
Secret reuse
valid audit
```

绝不删除用户未跟踪文件、备份、二进制。

输出：

```text
docs/AIOPS_CODE_AND_DEPENDENCY_CLEANUP.md
```

量化删除：

```text
files
LOC
routes
handlers
pages
APIs
dependencies
```

Gate 后停止。

---

# 八十一、Phase 15：依赖和镜像精简

## V9.3 Phase 15 详细执行任务树

### P15.1 Dependency Classification

逐服务将依赖分：runtime / build / dev / test / unused；新增依赖必须满足全局依赖纪律。

### P15.2 Python Runtime

拆 runtime/dev/test requirements，pin，`pip check`；prod image 不带 pytest/coverage/lint/notebook。

### P15.3 Go Runtime

`go mod tidy`、verify、vet；build 使用 trimpath/ldflags；CGO off 只有验证功能无损才允许。

### P15.4 Frontend Runtime

clean install、lockfile reproducible、dead deps removed、prod dist + minimal server；source map/diagnostic policy明确。

### P15.5 Docker Context / Multi-stage

按原文排除大目录；五服务都检查 non-root/read-only rootfs/explicit writable dirs，WAL/PVC 路径必须真实可写。

### P15.6 Security Smoke After Slimming

每个 image 验证：TLS CA、timezone/time comparison、DNS、health/readiness、WAL/recovery、K8s Secret mount、LLM HTTPS、VM/VLogs/CH/MySQL connectivity。

### P15.7 Image Accounting

对五 image 记录 baseline/final/delta/%/digest。总和必须 <=80%；达不到即 FAIL，不允许调整 Phase1 baseline。


Python：

```text
runtime deps
dev/test deps
pin versions
pip check
```

Prod image 禁止无运行必要的：

```text
pytest
coverage
lint
notebook
```

Go：

```text
go mod tidy
-trimpath
-ldflags "-s -w"
CGO off if feasible
```

Frontend：

```text
clean install
remove dead dependencies
runtime only dist + minimal server
```

Docker context 排除：

```text
.git
venv
node_modules
tests
coverage
docs
backup
archive
tmp
runtime dump
cache
old dist
```

所有自研容器：

```text
non-root
read-only rootfs where feasible
explicit writable dirs only
```

最终：

```text
FINAL_TOTAL_IMAGE_SIZE
<=
BASELINE_IMAGE_SIZE × 0.80
```

不得为了体积删：

```text
CA
timezone
runtime libraries
WAL
health
recovery
```

输出：

```text
docs/AIOPS_IMAGE_SIZE_REPORT.md
```

Gate 后停止。

---

# 八十二、Phase 16：全量自动化测试 + 12 固定 RCA 场景

## V9.3 Phase 16 详细执行任务树

### P16.1 Clean Test Environment

从 clean dependency/cache policy 起跑；记录工具版本和环境。不得依赖开发机偶然残留状态。

### P16.2 Full Build/Test Matrix

严格执行原文 Python/Go/frontend/Helm/Docker/scans，并加 race、contract fixtures、internal trusted-context matrix、schema/readiness checks、legacy path scan。

### P16.3 12 RCA Scenario Harness

每场景 fixture 定义 fault injection、tenant/cluster/resource、expected Intent、required/forbidden Tools、Evidence、Hypothesis、Contradiction、Missing、RootCause、confidence interval、unknowns。

### P16.4 Structure Assertions

禁止只比较自然语言答案。每场必须验证完整结构链和 exact ToolResult semantics。

### P16.5 Multi-source Boundary Auxiliary Tests

原 12 场不变；额外验证但不新增业务子系统：

```text
platform-self data can enter existing Query/Tool/Evidence chain
registered external source can enter same chain
unknown/unregistered canonical cluster or missing registered mapping rejected
source unavailable != no_data
same-name cross-cluster/resource isolation preserved
```

### Gate 16

原 12 场必须 12/12；多源接入辅助安全场景也必须 PASS，否则 Phase17 不得开始。



全量：

```text
Python tests
compile
pip check

Go tests
Go vet/static check

Frontend typecheck
unit/component
production build

Helm template
deployment check
clean Docker build

secret scan
binary scan
backup scan
runtime data scan
```

十二场景：

```text
OOMKilled
CrashLoopBackOff
service error rate
API P99
Redis timeout
Deployment unavailable
Node pressure
post-change failure
similar KB case
RBAC denied
Tool timeout
no data
```

每场自动断言：

```text
Intent
Cluster scope
Plan
Tool
ToolResult
Evidence
Hypothesis
Contradiction
Missing
Follow-up
RootCause
Confidence
Unknowns
```

禁止只看自然语言答案。

Gate：

```text
12/12 pass
```

否则停止。

---

# 八十三、Phase 17：Precise Historical Runtime Data Reset

## V9.3 Phase 17 详细执行任务树

**唯一 destructive Phase。任何普通“继续/可以”都不是授权。**

### P17.1 Maintenance Preconditions

逐项机器验证原文 preconditions；生成 snapshot evidence。任一不满足→BLOCKED。

### P17.2 Physical Object Enumeration

实际枚举 MySQL table/row class、CH table/partition、VM/VLogs retention object（如适用）、Chroma collections、MinIO prefixes/objects、PVC/WAL/checkpoint。禁止根据文档猜 actual name。

### P17.3 Manifest Classification

每对象逐字段填写；UNKNOWN 永不删除。V9.3 不新增 Incident/Detection persistence；所有删除仍只按既有 runtime/control-plane 对象和 Manifest 精确分类，不得误删 auth/config/knowledge。

### P17.4 Manifest Hash

canonical serialize manifest，生成 manifest_id + SHA256 + environment；授权请求必须引用 exact hash。

### P17.5 Exact Authorization Gate

只有原文精确格式或等价明确授权才执行。授权后 manifest 任何变化都需要新 hash/新授权。

### P17.6 Cleanup Transaction Runbook

maintenance→writers stop→WAL drain/preserve→active run/action recheck→逐对象 delete→每对象 post-check。任一失败 STOP，不继续剩余删除。

### P17.7 Restart / Fresh Data

按 manifest 允许的 init 操作恢复，启动 writers，验证 fresh telemetry only；不得恢复 old reader/legacy adapter。

### Gate 17

删除日志必须记录对象 ID/结果，不记录 secret/data payload；未经 manifest 的对象 deletion count 必须=0。


这是唯一正式 destructive cleanup Phase。

执行之前必须满足全部 Preconditions：

```text
no active investigation run
no executing action
no verifying action
no pending confirmation
no pending approval requiring preservation
all writers entered maintenance
all WAL/outbox drained or explicitly preserved
new schema/version verified
environment identified as local acceptance
```

若任何一项不满足：

```text
BLOCKED
```

不得清。

---

## 83.1 DATA_DELETION_MANIFEST

必须先生成精确 Manifest。

每项：

```text
object
storage
tenant_id
cluster_id
classification

actual name
size if available
last_write if available

active_writer
active_reader

reason
risk

decision
```

classification：

```text
OLD_SCHEMA_RESIDUAL
NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA
PERSISTENT_CONTROL_PLANE_DATA
```

decision：

```text
DELETE
PRESERVE
UNKNOWN
```

规则：

```text
UNKNOWN → NEVER DELETE
```

---

## 83.2 永久 PRESERVE

至少：

```text
users
roles
permissions
user_tenants
role assignments
tenants
clusters
credential_ref

valid authoritative sessions（当前唯一 Session authority）

Kubernetes Secrets
certificates

LLM config
data source config
platform config

valid knowledge
Runbook
SOP
knowledge source object
```

这些不能进入 DELETE。

---

## 83.3 允许纳入 DELETE 候选

```text
historical Metrics
historical Logs
historical Trace
RED
Topology
Alert
Resource Event
Change runtime history

AI Runs
Tool Runs
Evidence
Hypotheses
Actions
Verifications
AI Run Events

old Workflow runtime
checkpoint runtime

obsolete Chroma runtime collection
obsolete MinIO runtime prefix

runtime-only PVC
```

---

## 83.4 用户授权

执行前必须输出：

```text
manifest_id
sha256
environment
```

只有类似：

```text
确认执行 DATA_DELETION_MANIFEST:
manifest_id=...
sha256=...
environment=...
```

才算授权。

以下不算：

```text
继续
可以
按方案
确认 Phase 17
执行吧
```

---

## 83.5 Cleanup 顺序

```text
maintenance
→ stop active writers
→ drain WAL/outbox
→ verify no active run/action
→ cleanup Manifest-approved objects
→ verify schema/version
→ optional idempotent init only if Manifest explicitly says so
→ restart writers
→ verify fresh writes
```

任一对象删除失败：

```text
STOP
```

不得进入 Phase 18。

---

# 八十四、Phase 18：最新源码构建与部署

## V9.3 Phase 18 详细执行任务树

### P18.1 Intended Source Manifest

列出所有拟交付 tracked + intended source；对每文件 relative path + content SHA256 排序，生成 deterministic source_tree_hash。排除 mtime/absolute path/cache/log/runtime artifact。

### P18.2 Fresh Full Tests

从当前 source tree 重新执行 full required tests；不能引用 Phase16 旧结果。

### P18.3 Version / Build Identity

统一生成 version/build_id/build_timestamp/source_tree_hash；五 image label 与实施报告一致。

### P18.4 Five-image Clean Build

clean context build，记录 digest/size；再次检查 Phase15 80% 目标没有回退。

### P18.5 Deployment

部署到至少 existing cluster + aiops-kind-02；用 digest reconciliation 证明 running image 与本轮 build 一致。

### P18.6 Runtime Readiness

逐服务 health/readiness + MySQL/VM/VLogs/CH/Chroma/MinIO/LLM/K8s dependency checks；检查 Secret reference 存在性，不输出内容。

### Gate 18

两 Cluster rollout complete、所有 running image digest 对齐、无 old image、无 schema mismatch。


因为全过程禁止 commit，所以 source identity 固定使用：

```text
source_tree_hash
build_id
version
build_timestamp
image_digest
```

## source_tree_hash

必须基于：

```text
所有拟交付 tracked + intended source files 的确定性内容清单
```

排序后计算 SHA256。

不得使用：

```text
mtime
absolute path
temporary runtime artifact
logs
cache
backup
```

使同一源码内容产生不同 hash。

必须把具体算法记录到实施报告。

流程：

```text
current delivery source
→ fresh full tests
→ version increment
→ build all five images
→ same version
→ inspect images
→ deploy
→ rollout
→ health/readiness
→ dependency health
→ digest reconciliation
```

至少两个测试 Cluster：

```text
existing local cluster
aiops-kind-02
```

Gate 后停止。

---

# 八十五、Phase 19：真实新数据、多集群、真实 LLM、Browser E2E

## V9.3 Phase 19 详细执行任务树

### P19.1 Fresh Telemetry Generation

真实生成 Traffic/K8s events/trace/log/metrics/change，记录时间窗口与 test resource IDs。数据至少覆盖平台自身一个 runtime resource 和已注册外部 Kubernetes/基础设施 source 一个 resource。

### P19.2 Registered Source Acceptance

证明外部 source 必须先注册并通过 endpoint/credential/identity/capability validation 后才能进入生产 Query/Tool/Evidence 链；不存在主动扫描/发现未注册 Cluster 的路径。

### P19.3 Two-cluster Same-name Isolation

两 Cluster 都部署同名 `orders`（或等价），验证 metrics/logs/trace/topology/event 直到 Evidence/Hypothesis/RCA/SSE/Action/Verification 全链不串。可使用本平台所在 Cluster + 一个已注册外部测试 Cluster，或两个已注册测试 Cluster。

### P19.4 Platform-self Failure Analysis

至少构造一个平台自身故障/退化场景，例如 query-api/ingest/event-collector/backend unavailable，证明系统能区分：被运维对象 no_data、AIOps 自身 pipeline failure、backend unavailable、permission_denied、timeout。

### P19.5 Manual AI Trigger E2E

至少验证平台自身和已注册外部 source 各一条：

```text
Alert/Event/Change/异常事实已存在
→ wait/refresh
→ assert zero new AI Run
→ assert zero background LLM/K8sGPT/Planner invocation

user clicks/submits AI analysis
→ authorization PASS
→ exactly one user-triggered Investigation Run
→ Planner/Agents/LLM may continue within that Run
```

同时验证 SystemPrincipal/internal service credential 无法直接创建 AI analysis Run。

### P19.6 Real LLM Ten Questions

严格使用当前 provider/model，保存 run IDs、Tool/Evidence references 和结构化结果；Mock LLM 禁止最终验收。

### P19.7 K8sGPT

真实调用并准确说明 success/no_data/unavailable/permission semantics；不得伪称执行成功，不得泄漏 kubeconfig。

### P19.8 Browser E2E Three Roles

normal user / approver / administrator 真实走 login→professional pages→用户显式触发 AI investigation→evidence→confirmation/approval→execution→verification→history→SSE reconnect。

### P19.9 Tamper E2E

localStorage role、URL run/evidence ID、cluster selector、action request、registered-source mapping tamper 均由服务端阻止。

### Gate 19 追加断言

```text
platform-self + registered-external multi-source journey PASS
unknown/unregistered canonical cluster or missing registered mapping cannot become production fact source
no active infrastructure discovery required or present
same manually-triggered Run/Planner/Evidence/RCA chain used for both source classes
alerts/events/changes never start AI without explicit authenticated human action
```



必须通过真实数据链：

```text
Traffic
→ OTLP/DeepFlow/K8s Event
→ ingest/event collector
→ storage
→ query-api
→ frontend
→ Agent Tool
→ Evidence
→ RCA
```

两个 Cluster 中创建同名测试服务，例如：

```text
orders
```

必须验证不串：

```text
metrics
logs
trace
topology
events
evidence
hypothesis
RCA
SSE
approval
execution
verification
```

---

## 85.1 真实 LLM 十问

必须实际使用当前配置的 Provider/Model。

固定问题：

1. 当前 Kubernetes 健康状况是什么，给出真实证据。
2. orders 服务错误率为什么上涨。
3. 自动定位当前异常日志模式。
4. OOMKilled 的真实根因是什么。
5. 是否与最近变更有关。
6. 使用 K8sGPT，并准确说明 K8sGPT 实际执行结果。
7. 是否有相关 Runbook 或历史案例。
8. 当前还缺什么证据。
9. 给出处置方案但不要执行。
10. 修复后如何验证恢复。

另外必须验证：

```text
同名服务跨两个 Cluster 比较
指定 Cluster 调查
```

Mock LLM：

```text
禁止用于最终验收
```

---

## 85.2 Browser E2E

必须真实操作：

```text
login
overview
services
logs
traces
Kubernetes
alerts
investigation
evidence deep link
confirmation
approval
execution
verification
run history
SSE reconnect
```

至少：

```text
normal user
approver
administrator
```

三角色。

篡改 localStorage role：

```text
不得提权
```

Gate 后停止。

---

# 八十六、Phase 20：缺陷收口 + 最终生产构建

## V9.3 Phase 20 详细执行任务树

### P20.1 Defect Ledger

每项缺陷记录 id/severity/component/repro/root cause/fix/tests/regression/status。不得用“偶现/重跑通过”关闭安全 flaky。

### P20.2 P0/P1 Classification Review

继续使用原文 P0/P1 清单，并把以下归入既有安全/语义类缺陷：

```text
unknown/unregistered canonical cluster or missing registered mapping accepted as production fact source
registered mapping resolved to wrong tenant/canonical cluster
platform pipeline unavailable treated as target no_data/healthy
source bypasses Trusted Query/Tool/Evidence boundary
```

不新增 Incident/Detection/Autonomy/Edge-Governance 缺陷类别，因为这些不属于当前架构。

### P20.3 Zero-defect Gate

只有 P0=0、P1=0 才开始最终 production cycle。

### P20.4 Fresh Final Cycle

重新执行 full tests→new version/source hash→five images→deploy→fresh telemetry→real LLM→browser→two-cluster→platform-self + registered-external source smoke。禁止复用 Phase19 evidence。

### P20.5 Final Identity Evidence

记录 source_tree_hash/build_id/version/image digests/deployed version/smoke run IDs。

### Gate 20

所有 final evidence 必须来自 Phase20 本轮时间窗口和本轮 image digest。



P0 Defect：

```text
fabricated fact
Tool semantic mismatch
permission_denied treated absent
no_data treated healthy
RCA without Evidence
Log Agent not running
SSE broken
cross-cluster contamination
wrong-cluster action
authorization bypass
approval bypass
execution treated as recovery
verification wrong
old image deployed
main page unusable
```

P1 Defect：

```text
filter/time/cluster issue
evidence deep link
correlation
confidence
navigation not converged
old path remains
dead code/dependency
```

最终：

```text
P0=0
P1=0
```

然后必须重新执行：

```text
full tests
new version
rebuild five images
deploy
fresh telemetry
real LLM smoke
browser smoke
two-cluster isolation
```

禁止复用 Phase 19 结果。

输出：

```text
source_tree_hash
build_id
version
image digests
deployed version
smoke run IDs
```

Gate 后停止。

---

# 八十七、Phase 21：最终文档与 Git 准备

## V9.3 Phase 21 详细执行任务树

### P21.1 Architecture Reconciliation

最终架构文档必须与真实运行代码一致：平台自身/已注册外部数据源接入边界、storage SoT、internal APIs、writer/reader、Run、Agent/RCA/Execution/SSE 均更新；不得把未实现的 Incident/Detection/Edge Autonomy/Autonomy Level 写成正式架构。

### P21.2 Implementation Report

逐 Phase 记录 Gate evidence、commands/exit codes、deviations、blocked/resolved、source identity；不得写不存在的 commit SHA。

### P21.3 Test / Acceptance Report

汇总 Phase16/18/19/20，但明确每次测试轮次独立，不用旧 PASS 替代 final。

### P21.4 Cleanup / Image Reports

量化删除、依赖变化、image baseline/final/delta，说明安全能力没有因瘦身丢失。

### P21.5 Final DoD Cross-check

逐条对最终 DoD 做 PASS/FAIL/evidence。V9.3 新增检查只针对多源接入边界：

```text
platform-self source integrated
registered external source integrated
unknown/unregistered canonical cluster or missing registered mapping rejected
same Query/Tool/Evidence/RCA chain used
source unavailable/no_data/permission/timeout semantics exact
no unnecessary parallel subsystem introduced
```

### P21.6 Git Preparation Only

仅：status/diff/secret scan/binary scan/backup scan/intended file manifest/proposed message/remote verification。

禁止：git add/commit/push。

### Exit State

唯一允许：



至少输出：

```text
docs/AIOPS_AGENTIC_ARCHITECTURE.md
docs/AIOPS_AGENTIC_IMPLEMENTATION_REPORT.md
docs/AIOPS_AGENTIC_TEST_REPORT.md
docs/AIOPS_DATA_MODEL_REDESIGN.md
docs/AIOPS_MULTI_CLUSTER_ARCHITECTURE.md
docs/AIOPS_CODE_AND_DEPENDENCY_CLEANUP.md
docs/AIOPS_IMAGE_SIZE_REPORT.md
docs/AIOPS_FINAL_ACCEPTANCE_REPORT.md
```

实施报告必须记录：

```text
source_tree_hash
build_id
version
phases
deviations
blocked items
```

不得写"最终 commit SHA"，因为当前禁止 commit。

---

## 87.1 Git 规则

全过程：

```text
NO git add
NO git commit
NO git push
```

Phase 21 只允许：

```text
git status
git diff
secret scan
binary scan
backup scan
intended commit file manifest
proposed commit message
remote verification
```

最终状态：

```text
WAITING_USER_AUTHORIZATION_FOR_GIT_COMMIT_AND_PUSH
```

只有用户之后明确授权，才进入独立 Post-DoD Publishing Gate。

---

# 八十八、测试先行规则

所有功能修改必须遵守：

```text
1 read current implementation
2 add failing test
3 verify test fails for intended reason
4 implement minimum required change
5 focused tests
6 adjacent tests
7 remove obsolete path
8 retest
```

禁止：

```text
大面积先改实现
最后补几个测试证明"能跑"
```

---

# 八十九、依赖纪律

新增依赖只有同时满足：

```text
stdlib/current dependency cannot reasonably implement
version pinned
actively maintained
acceptable license
no known critical vulnerability
offline/cacheable
production image impact acceptable
```

新增后必须：

```text
update lock
cache artifact if project uses offline build
dependency health check
runtime verification
```

禁止为了简单功能引入大型框架。

---

# 九十、安全纪律

全过程禁止打印：

```text
Secret
Token
DB password
kubeconfig
private key
client certificate private material
LLM API key
```

报告只能记录：

```text
exists
reference/name
provider/source
```

不得记录内容。

测试 fixture 也不能放真实密钥。

---

# 九十一、历史数据总原则

历史运行数据：

```text
NO MIGRATION
NO CONVERSION
NO LEGACY ADAPTER
```

但：

```text
NO PHYSICAL DELETE BEFORE PHASE 17 AUTHORIZATION
```

Phase 6 后：

```text
old physical data may exist

old reader/writer must not exist
```

不能为了历史数据保留旧架构。

---

# 九十二、Phase 执行输出格式

V9.3 激活后，每个 **Phase 7–21** 完成时 DeepSeek 必须输出：

```text
PHASE:
STATUS: PASS / FAIL / BLOCKED

BASELINE/INPUT:
...

IMPLEMENTED:
...

FILES_ADDED:
...

FILES_MODIFIED:
...

FILES_DELETED:
...

COMMANDS_AND_TESTS:
- command:
  exit_code:
  result:

GATE_RESULTS:
...

SECURITY_RESULTS:
...

DATA_MUTATION:
NONE / exact description

P0_DEFECTS:
...

P1_DEFECTS:
...

KNOWN_LIMITATIONS:
...

DEVIATIONS_FROM_CONTRACT:
...

GIT_ACTION:
NONE

NEXT_PHASE:
NOT_STARTED
```

如果：

```text
STATUS != PASS
```

则：

```text
NEXT_PHASE=NOT_STARTED
```

不得自行继续。

---

# 九十三、遇到真实代码差异时的处理方式

如果本文写：

```text
模块 A
```

但实际代码中叫：

```text
模块 B
```

DeepSeek 应：

1. 在 Code Map 记录真实模块。
2. 明确 B 对应本文 A。
3. 在 B 中实现本文契约。
4. 不因为名字不同重新设计。

只有真正的架构冲突才允许：

```text
BLOCKER
```

报告必须包含：

```text
actual code fact
contract requirement
why impossible
minimum deviation
impact
```

不得自行批准偏差。

---

# 九十四、明确禁止 DeepSeek 做的事情

禁止：

```text
为了历史数据保留 legacy architecture

新旧两套 RCA 长期并存

新旧 Tool Router 长期并存

frontend 直接 orchestrator

Agent 直接 DB

Agent 直接 K8s API

orchestrator 持 kubeconfig

JWT 携带权威 role/scope

默认 tenant

默认 cluster

cluster=* internal context

Raw Logs 双主存

permission_denied → no_data

no_data → healthy

unavailable → healthy

LLM 推断 → live fact

LLM 风险 → authoritative risk

raw LLM shell → execute

R3/R4 self approval

exit code 0 → recovery

SSE disconnect → cancel Run

增加 timeout 代替 heartbeat/replay

Mock LLM → final acceptance

Mock data → final acceptance

删除 WAL/Precheck/Command Policy 为了简化

删 CA/timezone 为了减镜像

自动清历史数据

删除用户 untracked files

git add
git commit
git push
```

---

# 九十五、最终 Definition of Done

> **DoD 继承规则**：下列涉及 Phase 1–6 已建立能力的条目，只验证这些能力在最终系统中仍然成立，**不表示 V9.3 重新执行或重新验收 Phase 1–6**。其历史 PASS 仍来自 V9.2 FINAL R2；V9.3 Phase 7–21 只负责不得使这些不变量退化。

只有以下全部满足，才允许输出：

```text
AIOPS_AGENTIC_REFACTOR_COMPLETE
```

必须全部成立：

1. 五个自研服务完成重构。
2. 真多 Tenant Schema 生效。
3. User ↔ Tenant 多对多生效。
4. Cluster 单 Tenant ownership 生效。
5. Cluster canonical UUID 生效。
6. Resource ID 不含 Tenant。
7. JWT 无 role/scope。
8. MySQL 是 Authorization 唯一 Source of Truth。
9. 唯一 Session/token-version authority 生效；若已按版本迁移原子切换到 `auth_sessions`，则 `auth_sessions` 为唯一权威，否则沿用已通过 Gate 的当前物理权威。
10. 无 Authorization Cache。
11. RunInvocationContext 生效。
12. RunControlContext 生效。
13. TrustedRequestContext 生效。
14. 三类 Context 不混用。
15. Service Credential 与 signing key 分离。
16. nonce replay 防护生效。
17. System Principal 仅用于受控内部非 AI 调查用途，不能创建或启动 AI Run。
18. orchestrator 无 Kubernetes Credential。
19. per-cluster Secret 生效。
20. ClusterClientManager 不跨 Cluster 复用。
21. K8sGPT 不泄露 kubeconfig。
22. Raw Metrics 仅 VictoriaMetrics。
23. Raw Logs 仅 VictoriaLogs。
24. ClickHouse 职责符合合同。
25. AI Runtime 通过 query-api Persistence。
26. multi-cluster Run Model 生效。
27. Tool/Action/Evidence/Hypothesis 单 Cluster 约束生效。
28. Tool Registry 是唯一生产 Tool 入口。
29. ToolResult 语义准确。
30. Structured Query Boundary 生效。
31. Evidence 可追溯。
32. Evidence provenance 去重生效。
33. 七类 Agent 生效。
34. Log Agent 仅在用户人工触发的 Investigation Run 内按 Planner 自动参与，不得后台自行启动。
35. Resource Graph 权限过滤生效。
36. Hypothesis RCA 是唯一正式 RCA。
37. Missing Evidence 补查生效。
38. RCA 评分符合固定公式。
39. First Bad Event 生效。
40. Planner budget 生效。
41. Run State Machine 生效。
42. Optimistic CAS 生效。
43. SSE sequence 生效。
44. SSE heartbeat 生效。
45. SSE replay 生效。
46. Run cancel 语义正确。
47. Structured OpsAction 生效。
48. Authoritative Risk 由 query-api Policy Engine 计算。
49. R2 confirmation 生效。
50. R3/R4 approval 生效。
51. R3/R4 禁止自审批。
52. restricted_shell 不可由 Planner 自动选择。
53. Execution Adapter 生效。
54. Verification 强制。
55. regressed 阻断自动链。
56. Rollback 重新生成 Action。
57. 智能调查替代 AI Chat 主入口。
58. 六大导航完成收敛。
59. 专业页面可发起调查。
60. Evidence deep link 可回专业页面。
61. Log 页面具备异常模式。
62. Run URL 重新鉴权。
63. Workflow 降级后台。
64. 普通用户不能管理 Tool/Prompt/Provider/Workflow internals。
65. X-Tenant-ID compatibility 已删除。
66. 旧 AI Chat 主路径已删除。
67. 旧 prompt-only RCA 已删除。
68. 旧 Tool Router 已删除。
69. 旧 Session/Checkpoint 业务历史路径已删除。
70. 旧 Schema Adapter 已删除。
71. 无明显死接口、死 Handler、死页面、死依赖。
72. 五个 runtime image 总大小 ≤ baseline × 0.80。
73. Python 全测试通过。
74. Go 全测试/检查通过。
75. Frontend typecheck/unit/build 通过。
76. Helm/deployment check 通过。
77. Docker clean build 通过。
78. 12 个 RCA 固定场景全部通过。
79. 两 Cluster 同名资源隔离通过。
80. 真实 LLM 十问通过。
81. K8sGPT 实际语义正确。
82. Browser E2E 真实通过。
83. 三角色安全测试通过。
84. P0=0。
85. P1=0。
86. 最终部署运行最终镜像。
87. source_tree_hash 与 build/image digest 可对应。
88. Phase 17 未经 Manifest 授权没有发生 destructive cleanup。
89. 若执行 Phase 17，用户/权限/Secret/配置/有效知识均完整。
90. 最终文档完整。
91. 未执行 `git add`。
92. 未执行 `git commit`。
93. 未执行 `git push`。
94. 平台自身运行数据能够进入现有 Unified Query → ToolResult → Evidence → Hypothesis RCA 链。
95. 已注册外部 Kubernetes/基础设施数据源能够进入同一条 Query/Tool/Evidence/RCA 主链。
96. 未知/未注册 canonical Cluster 或缺少有效注册映射的外部来源不能直接成为生产事实源；不存在主动扫描/自动注册外部 Cluster 的生产路径。
97. 数据源注册复用 Tenant/Cluster/credential_ref/现有配置权威，不形成第二身份、授权或数据 SoT。
98. source unavailable、no_data、permission_denied、timeout 语义严格区分；平台自身 pipeline 故障不得被误判为目标对象 healthy/no_data。
99. 所有 AI 分析（包括根因分析、告警分析、AI 调查、K8sGPT/LLM 分析入口）只能由已认证用户人工显式触发；Alert/Event/Change 到达、页面加载、后台 worker、scheduler、SystemPrincipal 均不能自动创建或启动 AI Run。
100. 七类 Agent 使用统一受控执行方式，不存在自由 Prompt 直连 DB/Kubernetes/数据源的旁路。
101. Knowledge 继续使用 Chroma + MinIO 既有边界；正式知识写入必须人工 Review，当前重构没有自动 Learning Engine。
102. 当前平台不承担边缘自治、边缘节点生命周期治理、Kubernetes 集群生命周期管理或第二控制面职责。
103. 平台自身 + 已注册外部数据源至少各有一个真实场景完成 Explicit Human AI Trigger → Run → Planner → Evidence → Hypothesis RCA → Structured OpsAction → Authorization/Approval → Verification 闭环，并证明无人工触发时 zero AI Run / zero background AI analysis。

任意一项未满足：

```text
NOT_COMPLETE
```

必须同时输出：

```text
unfinished item
reason
evidence
next required action
```

---

# 九十六、最终执行原则

DeepSeek 必须始终遵循：

```text
先读真实代码，再改；
先建立真实基线，再判断；
先写失败测试，再实现；
外部用户身份来自 JWT；
内部 SystemPrincipal/服务调用身份来自 ServiceAuthenticator + 可信 Context；
授权事实来自 MySQL；
Tenant 必须显式；
Cluster 必须 canonical UUID；
Resource Identity 必须确定性生成；
跨 Cluster 必须拆分；
Agent 只调查；
Tool 才访问真实能力；
ToolResult 不能模糊语义；
Evidence 才是现场事实基础；
LLM 可以推理，不能制造现场事实；
Hypothesis 必须有支持和反证；
证据不足就 Unknown；
低置信度不自动修复；
写动作必须结构化；
风险必须由服务端重算；
R2 需要确认；
R3/R4 需要独立审批；
执行成功不等于恢复；
Verification 才决定是否恢复；
Regressed 必须停止自动链；
新生产路径生效后删除旧路径；
历史数据不迁移；
历史物理数据只在 Phase 17 按 Manifest 清理；
不泄露任何 Secret；
不删用户未跟踪资产；
不进行 git staging；
不进行 git commit；
不进行 git push；
一次只执行一个 Phase；
Gate 不过绝不进入下一 Phase；
数据源必须先注册再进入生产事实链；
所有 AI 分析必须由已认证用户人工显式触发；
Alert/Event/Change 只允许自动采集、存储、展示，不得自动启动 AI Run；
SystemPrincipal、scheduler、background worker 不得创建/启动 AI 分析 Run；
人工触发一次 Run 后，Planner/Agent/LLM/K8sGPT 可在该 Run 内按授权计划自动继续，不要求逐 Agent 二次点击；
禁止主动扫描/发现未注册外部 Kubernetes 平台；
平台自身与已注册外部 source 使用同一 Query/Tool/Evidence/RCA 语义；
source unavailable、no_data、permission_denied、timeout 必须严格区分；
Run 继续作为一次调查的唯一业务主对象，不新增平行 Incident 生命周期；
不新增独立 Detection Engine/Event Center；
不新增边缘自治或大规模边缘节点治理控制面；
Knowledge 正式写入仍必须人工 Review，不新增自动 Learning Engine；
七类 Agent 必须遵循统一受控执行方式；
R0–R4 风险、R2 confirmation、R3/R4 independent approval 继续作为唯一自动处置安全门。
```

最终产品只允许收敛成：

```text
平台自身运行数据 ───────────────┐
                                 ├→ Unified Query Layer / Tool Boundary
已注册外部 Kubernetes/基础设施 ─┘

Alert / Event / Change / Resource Context
→ display / select / prefill only
→ Explicit Authenticated Human AI Trigger
→ Investigation Run
→ Intent
→ Planner
→ Investigation DAG
→ Domain Agents
→ Tool Registry
→ Trusted Query Boundary
→ ToolResult
→ Evidence
→ Hypothesis RCA
→ Missing Evidence / Follow-up
→ Root Cause / Unknown
→ Structured OpsAction
→ Authoritative Risk
→ Confirmation / Approval
→ Execution
→ Verification
→ Run Completion
```

产品边界固定为：

```text
数据可以来自平台自身或已注册外部平台；
AIOps 负责统一接入、理解、调查、RCA、处置决策与验证；
资源控制权仍属于相应 Kubernetes/基础设施平台；
AIOps 仅通过既有授权边界和结构化 Execution Adapter 执行处置；
本平台不替代 Kubernetes 管理平台、边缘云控制面、CMDB 或节点治理平台。
```


**本 V9.3 DEEPSEEK EXECUTION R3 冻结后，DeepSeek 不再拥有重新设计已冻结架构的权限；后续只允许从 Phase 7 开始严格按 Phase 实施、验证、删除旧路径、记录偏差，并在真实架构冲突时请求用户决策。**
