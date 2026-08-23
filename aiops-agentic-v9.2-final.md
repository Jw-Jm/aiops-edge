# AIOps Agentic 全面重构最终强约束执行规格书
## V9.2 FINAL — DeepSeek V4 Flash Execution Contract

> **项目根目录**：`/Users/mssc/Documents/Code/agent/aiops/`
> **执行对象**：DeepSeek V4 Flash / Luna 类代码执行代理
> **实施范围**：整个 AIOps 项目，不局限于 AI Orchestrator
> **执行模式**：严格按 Phase 顺序推进；一次只允许执行一个 Phase；当前 Phase Gate 未通过时，禁止进入下一 Phase
> **Git 规则**：整个实施过程禁止 `git add`、禁止 `git commit`、禁止 `git push`
> **数据删除规则**：Phase 17 之前禁止任何未经单独授权的历史运行数据物理删除
> **最终目标**：把当前"页面 + 工具 + AI Chat + Workflow + 图谱 + 多套能力"的系统，重构为一条可信、证据驱动、可审计、可审批、可执行、可验证的 Agentic AIOps 主链

---

# 一、本文件的执行地位

本文件不是建议、参考架构、设计草稿，也不是供 DeepSeek 自由发挥的需求描述。

本文件是本次 AIOps Agentic 全面重构的**唯一执行合同**。

DeepSeek V4 Flash 必须遵循以下基本原则：

1. Phase 1 负责读取真实事实和建立基线。
2. Phase 2 负责冻结架构、数据所有权和所有核心契约。
3. Phase 2 Gate 通过后，DeepSeek 的角色从"设计者"切换为"严格实施者"。
4. Phase 2 以后不得重新选择 Tenant 模型、授权模型、Cluster Identity、数据存储、Tool 架构、Agent 架构、RCA 算法、Execution 边界等已冻结内容。
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
→ Incident Candidate
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
Incident Candidate / Learning
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
role_scope_assignments

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
5. 是否存在匹配的 role_scope_assignment
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

MySQL 必须存在权威 Session：

```text
auth_sessions

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

自动告警调查：

```text
principal_type=system
principal_id=alert-investigator
```

System Principal 权限仍来自 MySQL/Policy。

V1：

```text
system-triggered run = read_only
```

不得自动写。

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
HS256
```

每个方向独立 256-bit secret。

JWS Header：

```text
alg=HS256
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
Historical Incident              0.60
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

必须自动参与适用调查，不要求用户点击"分析日志"。

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
Incident
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

# 六十六、Incident Learning

成功 Run 可以生成：

```text
Incident Candidate
```

禁止自动写正式知识库。

必须人工 Review。

Review Approved 后才能进入正式 Knowledge。

---

# 六十七、Phase 1：冻结基线，建立真实地图

## 目标

只读盘点。

不改生产代码。

## 禁止

```text
源码修改
配置修改
依赖修改
数据库修改
运行数据修改
部署修改
删除文件
git add
git commit
git push
```

Phase 1 唯一允许的新 artifact：

```text
baseline 文档
为了镜像尺寸测量生成的临时 image/cache
```

临时镜像不得部署。

## 必须记录

```text
branch
HEAD SHA
git status
recent commits

kube-context
namespace
running pods
running images
services
endpoints
```

## 必须真实探测

```text
MySQL
ClickHouse
VictoriaMetrics
VictoriaLogs
ChromaDB
MinIO
K8sGPT
LLM Provider
Kubernetes API
Docker
Helm
Playwright
Go
Python
Node.js
```

结果只允许：

```text
AVAILABLE
UNAVAILABLE
UNKNOWN
```

禁止：

```text
config exists → AVAILABLE
403 → no_data
command missing → zero data
```

## 必须输出

```text
docs/AIOPS_REFACTOR_BASELINE.md
docs/AIOPS_CODE_MAP.md
docs/AIOPS_API_MAP.md
docs/AIOPS_DATA_MAP.md
docs/AIOPS_FRONTEND_MAP.md
docs/AIOPS_DEPENDENCY_MAP.md
```

## 镜像 baseline

对五个自研 runtime image 按当前正式 Dockerfile build。

记录：

```text
docker image inspect .Size
```

定义：

```text
BASELINE_IMAGE_SIZE =
sum(all five image inspect .Size)
```

最终目标：

```text
FINAL_IMAGE_SIZE <= BASELINE_IMAGE_SIZE × 0.80
```

## Gate 1

必须满足：

- 六份文档完整；
- 当前系统服务、接口、数据、页面、依赖、镜像可回答；
- 所有失败命令记录 command/exit code/error；
- Phase 1 未修改生产代码/配置/数据。

Gate 后停止。

---

# 六十八、Phase 2：冻结架构、所有权、契约

允许修改：

```text
architecture docs
contract definitions
schema/type definitions
serialization tests
frontend type contracts
```

禁止切生产路径。

必须输出：

```text
docs/AIOPS_AGENTIC_ARCHITECTURE.md
docs/AIOPS_DATA_MODEL_REDESIGN.md
docs/AIOPS_MULTI_CLUSTER_ARCHITECTURE.md
docs/AIOPS_CONTRACTS.md
docs/AIOPS_DATA_OWNERSHIP.md
```

必须冻结本文件全部：

```text
enum
Context
Run
ToolResult
Evidence
Hypothesis
OpsAction
Approval
Verification
SSE
API contract
```

三端：

```text
Python
Go
TypeScript
```

必须可互相解析同一 fixture。

Invalid fixture 至少覆盖：

```text
invalid enum
missing tenant
missing cluster
scope mismatch
invalid context type
invalid nonce
invalid Evidence reference
invalid Action
invalid Risk
illegal Run transition
```

Gate：

```text
Python contract tests PASS
Go contract tests PASS
Frontend typecheck PASS
```

Gate 后停止。

---

# 六十九、Phase 3：多 Tenant、多 Cluster、信任边界与 Resource Identity

实现：

```text
auth_sessions
user_tenants
role_scope_assignments
tenant_clusters

Cluster Registry
canonical UUID

credential_ref

RunInvocationContext
RunControlContext
TrustedRequestContext

ServiceAuthenticator
JWS signing
nonce replay defense

Resource Resolver
```

废弃：

```text
JWT role/scope
id=1 default tenant
default cluster
implicit cluster fallback
X-Tenant-ID authoritative semantics
```

`X-Tenant-ID`：

Phase 3 起仅可暂时作为 requested tenant scope。

不得承担授权。

第二 Cluster：

若当前只有一个本地 Cluster，使用：

```text
kind
```

创建：

```text
aiops-kind-02
```

只在 Phase 3 做。

## Gate 3

必须验证：

```text
same service name in cluster A/B → different resource IDs

missing tenant → reject
missing cluster → reject
ambiguous cluster → reject
unauthorized cluster → reject

revoked role → immediate denial
revoked tenant → immediate denial
revoked cluster → immediate denial
revoked session → immediate denial

tampered context → reject
expired context → reject
wrong issuer/audience → reject
nonce replay → reject
scope mismatch → reject
```

Gate 后停止。

---

# 七十、Phase 4：新数据模型与统一初始化

建立新结构。

**不物理删除旧历史数据。**

统一 Schema Init。

运行服务账号不允许 CREATE TABLE。

必须建立 Schema Version。

建立：

```text
AI Runtime tables
Cluster/Auth tables
ClickHouse new structures
VictoriaLogs labels contract
VictoriaMetrics labels contract
```

Raw Logs 只进 VictoriaLogs。

禁止完整 Raw Logs ClickHouse 副本。

Phase 4 不负责最终 writer cutover。

## Gate 4

```text
empty environment init PASS
second idempotent init PASS
runtime accounts without DDL permission can start
users/config/secrets unchanged
```

Gate 后停止。

---

# 七十一、Phase 5：Writer 实现与原子切换

重构：

```text
ai-apm-ingest-go
ai-event-collector
```

所有新写入必须有：

```text
tenant_id
cluster_id
resource_id
```

或明确：

```text
partial
missing_fields
```

禁止猜。

保留：

```text
WAL
append
ack
replay
compaction
bounded retry
health
graceful shutdown
```

Event Collector：

```text
single leader
single deployment
WAL/outbox
checkpoint key=tenant+cluster+source
```

## 关键切换规则

Phase 5 不允许长时间让"新 writer 写新 schema，但生产 reader 只读旧 schema"。

必须先：

1. 新 writer 在隔离/受控模式验证；
2. Phase 6 新 reader 已可被部署或 feature switch 已准备；
3. 在同一个受控 cutover window 中切 writer；
4. 立即完成 reader cutover；
5. 删除旧 writer/reader active path。

也就是说，Phase 5 和 Phase 6 是两个代码 Phase，但**生产 cutover 必须按一个原子窗口完成**。

Phase 5 单独结束时，如果 Phase 6 尚未准备好，不允许关闭生产旧 reader 导致系统不可见。

## Gate 5

```text
new writer tests PASS
WAL replay PASS
storage outage recovery PASS
event dedup/backlog observable
writer ready for atomic cutover
```

Gate 后停止。

---

# 七十二、Phase 6：Reader / Query Layer 与原子切换

重构 query-api 为统一事实查询层。

必须支持：

```text
resource
metrics
logs
traces
alerts
topology
kubernetes
changes
knowledge
```

复用底层 repository/query service。

禁止 duplicate SQL。

## Cutover

Phase 6 Gate 前执行生产原子切换：

```text
switch new writer active
switch new reader active
verify fresh data visible

stop old writer
stop old reader

remove old writer adapter
remove old reader adapter
remove fallback
```

Phase 6 Gate 后必须是：

```text
new writer ACTIVE
new reader ACTIVE

old writer ABSENT
old reader ABSENT
old active adapter ABSENT

old physical historical data PRESENT BUT UNREACHABLE
```

## Gate 6

验证：

```text
frontend/query/tool fact semantics consistent
no_data ≠ permission_denied
unavailable ≠ no_data
timeout ≠ generic network error
```

Gate 后停止。

---

# 七十三、Phase 7：Tool Registry、Evidence、Intent、Planner

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
system principal behavior
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

valid auth_sessions

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

每个 Phase 完成后 DeepSeek 必须输出：

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
9. auth_sessions 生效。
10. 无 Authorization Cache。
11. RunInvocationContext 生效。
12. RunControlContext 生效。
13. TrustedRequestContext 生效。
14. 三类 Context 不混用。
15. Service Credential 与 signing key 分离。
16. nonce replay 防护生效。
17. System Principal 生效。
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
34. Log Agent 自动运行。
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

DeepSeek V4 Flash 必须始终遵循：

```text
先读真实代码，再改；
先建立真实基线，再判断；
先写失败测试，再实现；
身份来自 JWT；
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
Gate 不过绝不进入下一 Phase。
```

最终产品只允许收敛成：

```text
Trusted Identity
→ Real-time Authorization
→ Tenant / Cluster / Resource Scope
→ Intent
→ Planner
→ Investigation DAG
→ Domain Agents
→ Tool Registry
→ ToolResult
→ Evidence
→ Hypothesis RCA
→ Missing Evidence
→ Root Cause
→ Structured OpsAction
→ Authoritative Risk
→ Confirmation / Approval
→ Execution
→ Verification
→ Incident Learning
```

**本 V9.2 FINAL 版本冻结后，不再允许执行代理重新进行架构设计；后续只允许严格按 Phase 实施、验证、删旧路径、记录偏差。**
