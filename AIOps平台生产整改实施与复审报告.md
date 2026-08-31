# AIOps 平台生产整改实施、架构与功能复审报告

**复审日期：** 2026-08-31（Asia/Shanghai）
**分支/基线：** `main` / `4036b46cf2e2ef94d4b8f0c71db3c39e555662af`
**本机验证：** OrbStack Kubernetes `orbstack`，Helm release `aiops` revision 7（2026-08-31 15:28 +0800），运行镜像标签 `git-4036b46cf2e2`（代码提交为 `2e02509`）
**工作区：** 存在本轮整改未提交变更；未重置、格式化或覆盖用户既有修改。

> 本报告是“代码整改后”的架构+功能复审，不把注释、路由定义或测试名称当成功能证据。结论只依据真实入口、调用链、配置/数据结构、测试输出和本机运行结果。生产环境未被连接，未使用生产凭据。

## 1. 审查结论摘要

| 维度 | 结论 | 结论依据 |
|---|---|---|
| 设计符合性 | **有限通过** | MySQL IAM/session/scope、HttpOnly Cookie、canonical UUID、签名 `TrustedRequestContext`、Query/Dispatcher/Alert/Worker 拆分、统一 Ingest、RCA V2、LLM Proxy 边界和生产 egress default-deny 清单已接入真实调用链；生产 Secret、证书身份/SAN、API Server CIDR、真实数据源和多副本演练仍缺证据。 |
| 功能完整性 | **有限通过** | AICHAT（Query `ProxyChat` → Orchestrator `/internal/v1/chat` → MySQL transcript）本机 SSE 闭环可用；RCA 图增强可运行但本机证据不足时返回 `partial/insufficient_evidence`；生产 Mock 已代码级 fail-closed，真实 Provider、真实 TokenRequest mutation、历史事件迁移未验证。 |
| 架构合理性 | **有限通过** | 服务边界和数据 owner 已明显收敛；Python `main.py` 仍保留重复 Chat/legacy 路由和兼容代码，细粒度 TLS SAN 配置与旧 scope 兼容路径仍有治理成本。 |
| 生产就绪度 | **不通过** | `collect-release-evidence.sh` 当前输出 `working_tree_dirty=true,publishable=false`；生产 Secret 尚未注入，真实 Provider、Graph 容量/恢复、Broker mutation、HA/备份/回滚仍无候选环境证据。 |

**当前不能发布到生产。** 最小阻断集合：

1. 生成提交和不可变镜像 digest，证据文件必须变为 `publishable=true`；
2. 通过 ExternalSecret/Vault/KMS 注入生产 Secret、每个服务的证书/CA/SAN 以及实际 Kubernetes API Server CIDR，完成双向 TLS 拒绝矩阵、NetworkPolicy 连通性和轮换演练；
3. 用真实指标、日志、追踪、告警和变更 marker 完成 Query→数据源的 canary，证明 RCA 的 partial 仅由证据不足导致，而不是数据源故障；
4. 用当前候选镜像完成 Graph schema/source/load/recovery/租户隔离门禁；
5. 若发布范围包含变更动作，再启用并验收 Credential Broker/TokenRequest；否则保持 Executor `disabled` 且将其作为门禁；
6. 完成多副本 nonce/replay、MySQL 备份恢复、升级/回滚和 SLO 证据。

没有已确认的 P0 级越权或数据破坏缺陷；P1 阻断项均在第 6 节给出可执行验收标准。

## 2. 审查范围与证据

### 2.1 设计基线读取结果

已读取并按“冻结设计 > ADR > 契约 > 代码”排序使用：

- `README.md`、`SECURITY.md`；
- `docs/architecture/index.md`、`docs/architecture/ADR-0001-control-plane-ownership.md` 及同目录 ADR；
- `docs/ownership/data-owners.md`、`docs/runtime-slo.md`、`docs/DEPLOYMENT_AND_VERIFY.md`；
- `AIOps_全面代码审阅报告.md`、`AIOps_全面代码修改报告_V2.md`、`AIOps_MySQL_HugeGraph_双存储生产化改造方案.md`；
- `docs/superpowers/specs/`、`docs/superpowers/plans/` 中最新 workflow、Graph、Action、DeepFlow、数据清理和部署方案；
- MySQL migration、ClickHouse 初始化/迁移、所有自研服务 README/测试说明；
- `deploy/helm/aiops/` 全部 values、Deployment、RBAC、NetworkPolicy、PDB、Job 和 `deploy/scripts/*contract*.sh`。

项目根目录没有 `AGENTS.md` 或当前有效的 `aiops-agentic.md`；`docs/archive/` 中的同名文件只作为历史材料，不覆盖当前 ADR。

### 2.2 检查范围

覆盖 `ai-orchestrator`、`ai-apm-query-go`、`ai-apm-ingest-go`、`ai-event-collector`、`ai-action-executor`、`ai-credential-broker`、`ai-llm-egress-proxy`、`observability-frontend`、MySQL/ClickHouse migration、Helm 部署、测试、脚本和文档。第三方依赖只检查版本/调用边界，未审查其业务实现。

### 2.3 实际执行的命令与结果

| 类别 | 命令 | 实际结果 |
|---|---|---|
| Query API Go | `cd ai-apm-query-go && go test ./...`；`go test -race ./...` | **全部通过**；新增 `NO_DATA` 和 SAN ToolRun/证书语义测试通过。 |
| 其他 Go 服务 | Action Executor、Ingest、Event Collector、LLM Proxy、Credential Broker 各执行 `go test ./...` 与 `go test -race ./...` | **全部通过**。 |
| Python 全量 | `AIOPS_DATA_DIR=<临时目录> ai-orchestrator/.venv314/bin/python -m pytest -q` | **1203 passed, 1 skipped, 2 warnings**；首轮系统 Python 3.9/沙箱权限错误已按约束改用项目 Python 3.14 和授权回环端口后重试。 |
| 前端 | `cd observability-frontend && npm run test:run && npm run build` | **25 files / 39 tests passed**；TypeScript/Vite 构建通过；仍有大 chunk、React Router future flag、Antd `destroyOnClose` 弃用警告。 |
| 工作流门禁 | `bash deploy/scripts/verify-aiops-workflow-gates.sh` | 首次受限沙箱运行因 Go `httptest` 回环监听被拒（`operation not permitted`）而中止；按授权在本机环境重试后 **通过**：Go、跨服务 workflow 4 tests、Python 1203、Executor、前端 39/build、Helm lint、生产安全开关、部署契约和 Graph load contract 均通过。 |
| 架构/部署契约 | `AIOPS_CONTRACT_ALLOW_TEST_SECRETS=true bash deploy/scripts/test-production-architecture-contracts.sh`；`bash deploy/scripts/test-deployment-contracts.sh` | **均通过**；此前 SAN 列表逗号解析失败已修正为 Helm 转义参数并重跑通过。 |
| 生产 egress 清单 | `helm template ... -f deploy/helm/aiops/values-prod.yaml --set networkPolicy.kubernetesApiCIDRs={10.0.0.0/8}`；无 CIDR 同命令 | 注入测试 CIDR 时渲染 **37 个 NetworkPolicy**，default-deny、角色白名单、HugeGraph/schema migrator 规则均存在且无旧 `app: query-api` selector；未注入时明确 Helm 失败（`kubernetesApiCIDRs must be injected`）。 |
| revision 7 运行态选择器与启动依赖 | `kubectl -n observability get networkpolicy -o custom-columns=NAME:.metadata.name,PODS:.spec.podSelector.matchLabels`；`kubectl -n observability get deploy ai-orchestrator ai-investigation-worker -o jsonpath=...`；Pod 重启计数 | **通过**；Query 相关 NetworkPolicy 均指向 `app=query-api-http`，Orchestrator/Worker 均有 `wait-for-query-api` initContainer，2 个 Worker 与 Gateway 业务容器重启数为 0。 |
| 本机发布门禁 | `bash deploy/scripts/validate-local-stack.sh`；`AIOPS_VALIDATION_DATA_MARKER=aiops-canary ... validate-observability-evidence.sh` | 工作负载、MySQL 17 个迁移、最小权限、Executor disabled、Worker 开关、HTTPS readiness、canary 全部通过；真实 metrics/logs/Kubernetes events 均 **PASS**，DeepFlow/依赖/RCA 无来源，按设计 **exit 2 / BLOCKED_BY_ENV**，不得视为发布通过。 |
| 启动竞态修复重跑 | `RELEASE_TAG=git-4036b46cf2e2 SKIP_IMAGE_BUILD=1 AIOPS_REUSE_K8S_TLS_SECRET=aiops-internal-tls bash deploy/scripts/local-validation.sh --reuse-k8s-secret aiops-secrets --skip-deepflow` | **通过基础设施与安全门禁**；revision 7 部署完成，Gateway/Worker initContainer 成功且业务容器重启数为 0；无 marker 时观测证据按设计 `BLOCKED_BY_ENV`。 |
| 生产 Mock 启动拒绝 | `cd ai-orchestrator && .venv314/bin/python -m pytest tests/test_llm_mock.py -q` | **11 passed**；`AIOPS_ENV=production,LLM_MOCK=true` 子进程在应用初始化前非零退出。 |
| Query 作用域回归 | `go test ./...`；`go test -race ./...`；`test-production-architecture-contracts.sh` | **全部通过**；伪造 `X-Tenant-ID` 的本机请求仍返回 MySQL active scope，架构契约 ARCH-105/106/107/108 通过。 |
| 发布证据 | `bash deploy/scripts/collect-release-evidence.sh /tmp/aiops-release-evidence-current.json` | 合同、架构、Helm lint、diff check 均为 pass；工作区仍 dirty，故 `publishable=false`。 |

### 2.4 OrbStack 实际运行证据

本机 Helm revision 7 的非敏感摘要：

- Query HTTP、Dispatcher、Alert Evaluator、Orchestrator、Investigation Worker（2 副本）、Ingest、Event Collector、LLM Proxy、Action Executor、Frontend，以及 MySQL/ClickHouse/HugeGraph/VictoriaMetrics/VictoriaLogs 均为 Ready/Running；初始化与迁移 Job 为 Complete。
- 所有 9 个需要内部身份的 Deployment 均实际注入非空 `AIOPS_TLS_CLIENT_SAN`；Go 服务以 `AIOPS_MTLS_REQUIRED=true` 启用证书校验，Query 的 HTTPS readiness 通过。临时本地证书由验证脚本生成，未写入仓库。
- 无客户端证书访问 `POST /internal/v1/query/graph` 返回 HTTP 401；有效本地调用链可通过 mTLS、方向 token、签名 context 和 replay 校验。错误 SAN、过期证书、轮换和跨副本矩阵仍未在候选生产环境演练。
- 本机 release 保持 `EXECUTION_MODE=disabled`、`realMutation=false`，未调用任何 mutation endpoint；`credentialBroker` 的生产 mutation profile 未开启。
- 本机 revision 4 使用 `values-local-validation.yaml`，因此没有把生产全局 egress deny 应用到正在运行的 canary；生产 `values-prod.yaml` 已改为 `egressDefaultDeny=true`，并要求发布系统注入 API Server CIDR。生产模板渲染和 fail-closed 契约已通过，CNI 实际连通性仍须候选集群验证。
- revision 7 运行态 NetworkPolicy 已核对：`allow-frontend-to-query-api`、`allow-orchestrator-to-query-api` 和 `allow-query-api-to-hugegraph` 的目标标签均为 `app=query-api-http`；namespace 内没有旧的精确 `app=query-api` 目标。生产 egress 白名单规则因 local profile 显式关闭全局 egress deny，未在本机运行态启用。
- Orchestrator Chat Gateway 使用 `LLM_MOCK=true`，Worker 调查 runtime 独立运行；因此本机 AICHAT/RCA 证明的是边界和持久化，不是外部 Provider 成功率。revision 7 的 Gateway/Worker 均通过 `wait-for-query-api` initContainer 后启动，业务容器重启数为 0；应用级依赖竞态已在本机修复，但生产多副本/故障转移仍未验证。

### 2.5 本机端到端/隔离证据

- AICHAT canary：完成管理员登录、MySQL tenant/cluster scope 选择后，`POST /api/v1/ai/chat` 返回 HTTP 200、`text/event-stream`，收到 **24 个 SSE 事件、1 个 done、0 个 error**；`GET /api/v1/ai/sessions` 返回 HTTP 200 且结构有效。另用伪造 `X-Tenant-ID` 请求 `/api/v1/me`，服务端仍返回 DB active scope。未打印 token、密码或完整响应。
- 真实观测 canary：通过 Ingest mTLS + API key 写入 1 条 OTLP 日志和 1 条 OTLP Trace（marker 为 `aiops-local-real-20260831`，tenant/cluster 为本地 canonical UUID），Query/VictoriaLogs 返回该日志，Query/VictoriaMetrics 返回 1 次 call/error 聚合；Kubernetes Events 读取到 `aiops-canary`。门禁结果为 metrics/logs/events **PASS**，DeepFlow、service dependency、RCA evidence **BLOCKED_BY_ENV**，总退出码 2。
- RCA Run canary（Run ID `e82ff86b-abb1-4929-b0c7-3c94df2bf8f4`，显式创建、`target_type=service`）：创建 HTTP 201；最终状态 `partial`、`state_version=4`；独立 ToolRun 接口 HTTP 200、8/8 `success/complete`，Evidence 接口 HTTP 200、6 条记录。`partial` 仍然正确表示完整 RCA 需要的 alerts/changes/DeepFlow 等证据未齐，不能把单条 metrics/logs canary 抬高为根因确认。
- HugeGraph 已通过 typed loader 在本机写入 2 vertices/1 edge，Query `neighbors` HTTP 200 且返回 `DEPENDS_ON`、tenant/cluster 均匹配。200k/1M 生产负载只执行了 dry-run shape check，真实容量、p95、恢复仍未验证。
- no-data 回归已修复：`internal_query.go:288-313` 和 `316-356` 将授权的 `query.NoDataCode` 持久化为 `complete` 空 envelope；新增 `internal_query_test.go` 两个测试覆盖通用工具和 metrics 特殊路径。修复后上述 8 个 ToolRun 不再出现旧的 failed/no-data 语义。

### 2.6 环境限制

没有生产访问、生产凭据、真实外部 LLM key、真实多节点集群或企业 StorageClass；本机只写入了隔离 OrbStack 的 canary 观测和 2/1 Graph 数据，未执行生产迁移、外部系统写入或生产动作。不能据此推断证书轮换、TokenRequest、ClickHouse 合并、Graph 恢复、PITR 或真实 Provider 通过。

### 2.7 初始审查问题逐项核对

| 初始问题 | 当前结论 | 代码/运行证据 | 仍需完成 |
|---|---|---|---|
| P1-01：未选集群时 Chat 提交 `cluster_id=all` 导致不可用 | **已修复（本机验证）** | `observability-frontend/src/pages/ai/AiChat.tsx:163-189` 在发送前要求 canonical scope；Query `settings.go:941-1037` 继续拒绝 `all`/越权 scope；本机管理员登录、scope 选择后 SSE 24 events、1 done、0 error | 多副本并发与真实 Provider 证据仍属 P2-04 |
| P1-02：自动处置/写权限可能被过早打开 | **安全默认已修复；真实动作未发布** | `values-prod.yaml`、Executor `main.go`、Broker profile 均默认 `disabled`/`realMutation=false`；本机 safety gate 通过且未调用 mutation | 若纳入发布范围，完成 P1-05 的 Broker/TokenRequest/审批/审计验收；否则持续保持 disabled |
| P1-03：NetworkPolicy 非默认拒绝且 Query selector 错误 | **代码与本机 selector 已修复；生产 CNI 未验证** | `values-prod.yaml:86-88` 开启 egress default-deny；`networkpolicy.yaml`/`graph-networkpolicy.yaml` 使用 `app=query-api-http`；生产 Helm 渲染 37 个策略、缺 API CIDR 时 fail-closed；revision 7 运行态 selector 核对通过 | 候选集群注入真实 API Server CIDR，执行连通性/拒绝矩阵 |
| P1-04：真实 Agent 能力未成为默认主链 | **主链已接线；能力仍部分实现** | Query Run→Outbox→签名 RunInvocation→Orchestrator/Investigation Worker；`investigation_app.py` 不再 import `main`；本机 RCA Run 8/8 ToolRun complete | DeepFlow/依赖等完整证据、真实 Provider 和容量门禁仍未通过；legacy Chat/flow 清理见 P2-01 |
| P1-05：前端遗留入口与新网关兼容性不足 | **核心路径已收敛；遗留封装仍存在** | Query `ProxyChat` 是浏览器 Chat 入口；legacy suggestion/execute 路由由 production gate 关闭；前端与 Go/跨服务合同测试通过 | 完成 route inventory、删除/编译隔离旧 public handler 和 SQLite owner |
| P1-06：HA、重启恢复和断线回放未用运行证据确认 | **代码具备基础闭环；生产能力未验证** | MySQL Run/Chat/Outbox/lease/replay 结构、Worker 2 副本和本机 revision 7 Ready；Gateway/Worker initContainer 后无重启；本机为单节点 RWO | 多节点故障、SSE resume、PITR、RPO/RTO、升级/回滚和跨副本 replay 演练 |

## 3. 实际架构还原

### 3.1 代码实际控制流、数据流与信任边界

```mermaid
flowchart LR
    B[Browser\nHttpOnly Cookie + active scope] --> Q[query-api-http\nHTTP/Auth/Query/Run/Chat/Action]
    Q --> M[(MySQL\nIAM/session/scope\nRun/Chat/Action SoT)]
    Q -->|ai.chat signed context| O[ai-orchestrator gateway\n/internal/v1/chat]
    Q --> D[query-run-dispatch\noutbox lease/dispatch]
    D -->|ai.investigate JWS + nonce| W[investigation-worker\nstateless RCA/runtime]
    W -->|strict internal query| Q
    Q --> E[query-alert-eval\nDB-time leader/cooldown]
    W --> P[LLM egress proxy\nprovider allowlist/key isolation]
    P --> L[(External LLM Provider)]
    C[event-collector\nK8s/SEL adapter] -->|15-column envelope + event_id| I[unified ingest\nWAL/fsync/replay]
    I --> CH[(ClickHouse\nobservability SoT)]
    I --> VM[(VictoriaMetrics/VictoriaLogs)]
    Q --> CH
    Q --> G[(HugeGraph\nrebuildable projection)]
    Q -->|credential_ref| CB[credential-broker]
    CB -->|<=300s TokenRequest| K[(Kubernetes API)]
    Q --> X[ai-action-executor\nsigned action; disabled by default]
    X --> CB
```

**信任边界：** 浏览器只持 HttpOnly Cookie；Query 从 MySQL 读取用户/会话/租户/集群，再签发短时 `TrustedRequestContext`。内部路由还要求服务 token、JWS、audience/capability/scope、nonce/expiry；TLS 配置已加入所有内部服务，但 SAN allowlist 和轮换仍须候选环境验证。

**数据所有权：** Query/MySQL 是 IAM、Run、Chat、Action 的 owner；Ingest 是 telemetry 唯一写入口；ClickHouse 是观测事实存储；HugeGraph 是可重建投影；Orchestrator 仅作编排/语言交互，Worker 不直接读数据库、Kubernetes 或 Provider key。

### 3.2 文档设计与代码实际差异

| 设计 | 当前代码实际 | 结论 |
|---|---|---|
| Query HTTP、Dispatcher、Alert Evaluator 独立 | `cmd/api`、`cmd/run-dispatcher`、`cmd/alert-evaluator` 与 Helm 三 Deployment 均存在 | 已符合；API 扩容不会直接增加 outbox/evaluator 处理器。 |
| Worker 独立组合根 | `ai-orchestrator/apps/investigation.py:1-37` 初始化 Tool Registry 后直接导入 `orchestrator`；`investigation_app.py:1-12` 只作兼容 ASGI wrapper，不导入 `main` | 已修复；旧报告“Worker 导入 main”已失效。仍需静态规则防止回归。 |
| Chat 与 Investigation 分离 | Query `ProxyChat` 走 Orchestrator `/internal/v1/chat`；Run dispatcher 走 Worker `/internal/v1/run-invocations` | 已符合；Gateway 的旧 `/api/v1/ai/chat` 在 production 返回 410。 |
| 单一 RCA V2 | Worker 调 `RCAEngineV2`，Graph/evidence 统一经 Query internal tools；旧 `main.py` 仍保留 legacy helper | 运行时已符合；代码清理尚未完成。 |
| mTLS 服务身份 | Go/Python TLS listener、client transport、Helm cert mount 和 `ssl-cert-reqs=1` 已实现 | 代码/配置存在；证书来源、SAN 绑定、轮换、跨副本握手未验证。 |

## 4. 设计—模块—数据—调用链—测试追踪矩阵

状态定义：**完整实现**=真实入口、配置/数据结构、调用链和测试证据齐全；**部分实现**=代码闭环但存在生产/环境/兼容限制；**未实现**=入口或必要实现不存在/明确关闭；**未验证**=必须依赖外部环境，项目证据不足，不能推断通过。

| 设计要求 | 预期行为 | 实现位置与真实入口 | 配置/数据 | 测试证据 | 状态 | 偏差与结论 |
|---|---|---|---|---|---|---|
| MySQL 是用户、角色、租户、集群授权唯一权威 | 每次请求从 MySQL 校验用户/会话/成员关系；DB 不可用 fail-closed | `ai-apm-query-go/internal/api/auth.go:317-367,425-432`；`UserDAO`、`ClusterDAO` | `users`、`auth_sessions`、`tenants`、`user_tenants`、`clusters` | Go authz/login/race tests | **完整实现** | JWT 只提供身份句柄；旧兼容响应中的角色仅展示，不授权。 |
| JWT 只含用户、会话、短期声明 | 不承载 role/permissions/tenant scope；浏览器只收 HttpOnly Cookie | `auth.go:197-235,542-557`；frontend Login/ChangePassword | `auth_sessions.token_version` | `login_session_test.go`、`password_test.go`、前端 Login test | **完整实现** | 非浏览器 Authorization 仅为受控迁移兼容。 |
| 禁止 `X-Tenant-ID`、JWT role、默认 tenant/cluster 隐式回退 | scope 必须由 active MySQL scope 或显式 canonical ref 得到，缺失拒绝 | `auth.go:317-366`；`handler.go:300-312`；`settings.go:948-976`；`main.py:1580-1625`；frontend `AiChat.tsx:185-189` | active session scope、canonical UUID | `TestRequestAuthorizationContextIgnoresClientTenantHeader`、Query full/race、production architecture contract | **完整实现（生产运行时边界）** | Query/orchestrator 不再读取 caller 租户头或固定租户回退；Collector/DeepFlow 的 `x-tenant-id` 仅是固定写入协议；测试 fixture/历史兼容文件中的 `default` 不进入生产授权路径，仍由 P2-01 清理治理。 |
| `cluster_id` 不可变 UUID、slug/name 唯一 | 入口将 ref 解析为 UUID，所有 Run/Chat/数据按 UUID 隔离 | `ClusterDAO.ResolveRef`；`runs_public.go:99-138` | `clusters.cluster_id`、slug/name unique | cluster/run tests | **部分实现** | 约束和历史数据迁移脚本存在；生产数据库迁移未在本轮执行。 |
| K8s 凭据经 `credential_ref` 统一读取 | Orchestrator/Executor 不持原始 kubeconfig；Broker 按 profile 发 ≤300s TokenRequest | `ai-orchestrator/credential_broker.py:1-62`；`ai-action-executor/main.go:445-497`；`ai-credential-broker/main.go:145-190` | Broker profile ConfigMap、`clusters.credential_ref` | Broker/executor unit/race tests | **部分实现** | profile/TokenRequest 真实 API 未验证；生产 profile 默认关闭 mutation。 |
| 编排只通过签名 `TrustedRequestContext` 访问 Query | token + EdDSA JWS + capability + scope + nonce/expiry；body 不得覆盖 scope | `internal_query_envelope.go:65-127`；`internal_ingress.py:68-117`；`trusted_context_issuer.py:45-103` | signing/verify Secret、replay cache、tool registry | internal query/replay/context tests；无证请求本机 401 | **完整实现** | mTLS 是额外传输身份门禁，实时证书矩阵未验证。 |
| 内部服务身份认证、短签名、防重放、审计 | 服务证书、方向 token、唯一 nonce、TTL、审计 ID 可关联 | `bootstrap/mtls.go:13-88`；各 Go 服务 `mtls.go`；`ai-orchestrator/mtls.py`；`mysql_replay_cache.go` | TLS Secret、`AIOPS_TLS_CLIENT_SAN`、nonce/replay、audit tables | Go/Python replay tests、Helm render、revision 4 实际 env、无证书 HTTP 401 | **部分实现** | Go 服务已执行 SAN allowlist；Python uvicorn 只要求客户端证书/CA，尚未在应用层按 SAN 做逐服务 allowlist；跨副本 replay、轮换和撤销未验证。 |
| 授权落实到 tenant/cluster/namespace/resource/action | Internal tools 固定 capability；Action/Broker 重新校验 target、namespace、operation、credential_ref | `internal_query_envelope.go:27-43`；`ai-action-executor/main.go:289-337`；Broker `main.go:164-177` | `tool_runs`、`ai_actions`、Broker profiles | action/boundary tests | **部分实现** | 只读 Query 已闭环；真实 mutation 因本机/生产 disabled 或未提供 K8s evidence。 |
| Query/领域代理/Run/Chat 数据所有权一致 | 浏览器只通过 Query；Run/Chat/Action 落 MySQL；Worker 不做 owner | `ai_chat_sessions.go:18-216`；`store/ai_chat_sessions.go:10-182`；`runs_public.go` | `ai_chat_sessions/messages`、`ai_runs/outbox/events` | Go chat/run tests、Python full | **完整实现** | Gateway 仍保留仅迁移用途的 SQLite helper，未被 canonical browser path 使用。 |
| AICHAT 两个自研模块真实可用 | 前端登录/scope/SSE/会话/报告与 Query→Orchestrator 真实链路闭环 | `observability-frontend/src/pages/ai/AiChat.tsx:91-109,163-265`；`settings.go:920-1109`；`main.py:1064-1207` | MySQL chat tables；`ai.chat` signed context | 本机真实登录/scope 后 HTTP 200、24 SSE events（1 done/0 error）；sessions HTTP 200；frontend 39 tests；伪造租户头不改变 active scope | **部分实现** | 本机使用 mock/deterministic backend；真实 Provider、双 Agent、断线重连跨副本未完成候选环境验收。 |
| RCA V2、实体、证据、矛盾与 policy digest | Graph candidate + typed evidence + provenance + contradiction；数据不足返回 partial | `rca_engine/candidates.py:7-33`；`entity_resolver.py`；`runtime.py:54-154`；`contradictions.py` | `ai_run_graph_contexts`、`ai_evidence`、`ai_hypotheses`、policy JSON | 20 个 RCA targeted tests、Python 1203 全量、本机 partial Run | **部分实现** | 图增强成功；多类观测数据在本机 unavailable，不能声称根因完整。 |
| 固定 Run 时间窗口和 target_type | 创建时冻结 `[start,end]`，最长 24h；worker 不以自身时钟重锚 | `runs_public.go:76-138`；`run_dispatch.go:113-141`；Worker `apps/investigation.py:91-108` | `ai_runs.time_range_start/end,target_type` | Go run/dispatch tests；本机 Run persisted node/window | **完整实现** | 明确窗口错误返回 422；默认窗口可用 `AI_RUN_DEFAULT_WINDOW_MINUTES` 调整。 |
| Collector 不建表、不直写 ClickHouse | Collector→Ingest，WAL fsync 后 receipt；Ingest 是唯一事件写入口 | `ai-event-collector/clickhouse.go`、`wal.go`；`ai-apm-ingest-go/cmd/ingest/event_wal.go` | ClickHouse `k8s_events.event_id`、migration 0006/0007 | Go race/WAL tests、contract scripts | **完整实现** | 历史旧 writer/空 event_id 行仍需受控迁移。 |
| 事件至少一次与业务幂等 | 稳定 SHA-256 event_id；重复 replay 不重复计数 | Collector event ID、Ingest 15-column validation、CH ORDER BY | `k8s_events` versioned DDL | WAL/idempotency tests | **部分实现** | 新路径完整；历史行回填覆盖率和真实 merge 未验证。 |
| Graph 是可重建投影且有资源门禁 | HugeGraph schema/source/load/recovery/tenant isolation 证据需绑定候选 commit/digest | `kg_api.py`、`kg_graph.py`；`graph-networkpolicy.yaml:13-23` | Graph schema/dataset/recovery manifest | Graph contract scripts；本机 bounded query | **部分实现** | selector 已修正为 `query-api-http`；真实候选数据集/恢复/p95 门禁未完成。 |
| 生产 egress 默认拒绝且按角色白名单 | `values-prod` 必须打开 default-deny；Query/Dispatcher/Alert/Frontend/Worker/Graph/Executor/Broker 只能到声明的内部目标；Kubernetes API 通过注入 CIDR 放行 | `deploy/helm/aiops/values-prod.yaml:86-88`；`templates/networkpolicy.yaml:1-1195`；`templates/graph-networkpolicy.yaml:1-97` | `networkPolicy.kubernetesApiCIDRs`、NetworkPolicy selectors/ports | production architecture contract、Helm render/PyYAML parse、workflow gate | **部分实现** | 代码/清单已修复并通过静态门禁；本机运行的是 local-validation（egress 未全局开启），生产 CNI、CIDR、NetworkPolicy 实际连通性尚未验证。 |
| LLM 出站唯一经 Proxy | Orchestrator 只拿 provider metadata/Proxy token，不接 key/任意 URL；生产 Mock 必须 fail-closed | `tools.py`；`orchestrator.py`；Proxy `main.go:92-142`；`main.py` 生产启动 guard | Proxy Secret/provider allowlist | `test_llm_proxy_boundary.py`、`test_llm_mock.py`、Go race | **部分实现** | 本机 proxy/provider canary 未使用真实 key；外部网络、限流、熔断未验证。 |
| 生产部署、回滚、观测达标 | Secret/render/image digest、PDB、health/SLO、故障和回滚 evidence 完整 | Helm templates、`runtime-slo.md`、`collect-release-evidence.sh` | Secret refs、PDB、WAL PVC、release JSON | Helm/contracts/evidence script；revision 7 Ready；validator metrics/logs/events PASS、其余 BLOCKED | **未验证** | 本机 readiness 和合同通过不等于生产 HA、PITR、证书轮换、完整观测或 rollback 通过。 |

## 5. AICHAT 两个自研模块复审与改进方案

### 5.1 是否真实可用

结论：**本机只读对话功能真实可用，生产真实模型能力尚未验证。**

真实调用链如下：

1. `AiChat.tsx` 加载 `/ai/sessions`，从服务器获得 scoped session；发送时使用 `credentials: 'include'`，只提交 canonical `cluster_id`、message 和 session ID（`AiChat.tsx:163-189`）。
2. Query `ProxyChat` 校验 JWT/MySQL user、tenant membership、cluster ownership、`ai.chat` capability，创建/复用 `ai_chat_sessions` 并写入用户消息（`settings.go:941-1037`）。
3. Query 签发带 nonce/TTL 的用户 `TrustedRequestContext`，通过 mTLS client transport + `X-Internal-Token` + `X-Trusted-Request-Context` 调 `/internal/v1/chat`（`settings.go:1040-1074`）。
4. Orchestrator internal ingress 校验服务 token、JWS、audience、capability、scope、replay 后调用 `brain.stream_sync`，只读对话不会创建 Investigation Run（`main.py:1064-1132`）。
5. Query 不缓冲 SSE，逐帧写入浏览器并只把 assistant done/suggestion 持久化到 MySQL（`settings.go:1080-1109,1131-1160`）。前端随后刷新会话列表并可调用 Query-owned final report。

本机证据是完成真实登录/scope 后 HTTP 200、24 个 SSE event（含 1 个 done、0 个 error）和 Query-owned 会话接口 200；伪造 `X-Tenant-ID` 不改变服务端 active scope，因此不是“只有接口定义”。但当前 deployment 的 `LLM_MOCK=true`，且没有真实 Provider key，不能把 deterministic/mock 输出认定为真实模型可用。

### 5.2 真实缺口和可执行改进

- **Provider 可用性：** 在候选环境注入 Proxy provider profile、短 token、超时/限流/熔断配置；用固定 canary prompt 验证 200/SSE、Provider 429/5xx/timeout、密钥轮换和脱敏日志。UI 必须显示 `provider_unavailable`，不能静默伪装成功。
- **代码重复：** `main.py:1064-1207` 与 `main.py:1234-1356` 存在两套 thread/queue/SSE 逻辑。canonical 生产流量应只保留 Query Proxy→`/internal/v1/chat`，legacy 实现迁移后删除或编译隔离；验收是 production route table 不含 legacy handler，静态依赖不再引入 SQLite session owner。
- **并发幂等：** `AIChatSessionDAO.EnsureSession` 当前已由 MySQL session 表和唯一 session 标识承载，但本轮没有跨副本并发 20 首轮的运行证据。应补充唯一约束/幂等 upsert 压测，确保同一用户/tenant/cluster 只有一个 session owner，其他请求复用且不返回 500。
- **SSE 可靠性：** 保持 5 分钟 upstream deadline，增加 request/session/event sequence、heartbeat、断线取消和跨副本 resume 的集成测试；禁止把 progress/tool telemetry 写成永久 transcript。
- **数据边界：** final report 继续只读 Query/MySQL transcript；Action suggestion 只能创建 canonical Action proposal，不得重新启用 Orchestrator shell/K8s 直执行。

## 6. 问题清单（按 P0–P3）

### 本轮已完成并验证的修复

- **NO_DATA ToolRun 语义：** `ai-apm-query-go/internal/api/internal_query.go:288-299`（通用工具）和 `337-346`（metrics 特殊路径）把授权的 `query.NoDataCode` 转换为 `complete` 空 envelope，并写入正常完成的 ToolRun；`internal_query_test.go` 的两个回归测试覆盖这两条真实入口。本机 RCA Run 的 8/8 ToolRun 均为 `success/complete`、6 条 Evidence，证明修复已进入 revision 7 运行时。
- **内部服务 SAN：** `deploy/helm/aiops/templates/_helpers.tpl:12-24,174-181` 在 required mTLS 时强制注入 `AIOPS_TLS_CLIENT_SAN`；本地验证 profile 提供服务 DNS SAN；Go listener 的 `VerifyConnection` 对 DNS/URI SAN 做 allowlist 校验并在缺失配置时启动失败。revision 7 的 9 个 Deployment 均实际有非空配置，且无客户端证书内部探针返回 401。生产仍需将共享本地列表替换为逐服务身份并补 Python listener 校验。
- **契约脚本参数：** `test-production-architecture-contracts.sh` 与 `verify-aiops-workflow-gates.sh` 的逗号分隔 SAN 参数已按 Helm 语法转义；修复后两个脚本和完整 workflow gate 均通过。
- **Query 作用域与硬编码租户回退：** `auth.go:317-366` 现在只读取 `auth_sessions` 的 MySQL active scope；`handler.go:300-312` 的后台指标租户未配置时返回空并跳过 ETT，不再使用固定 UUID；`main.py:1580-1625` 的 legacy mutation 也只接受签名 context。`TestRequestAuthorizationContextIgnoresClientTenantHeader`、`TestMetricsTenantIDFailsClosedWithoutConfiguredSystemTenant`、Query full/race 和 ARCH-105/106/107/108 均通过。
- **生产 NetworkPolicy 默认拒绝与选择器：** `values-prod.yaml:86-88` 已将 `egressDefaultDeny` 设为 `true`；`templates/networkpolicy.yaml:177-357,603-630,1104-1195` 补齐 Dispatcher/Alert/Frontend/Executor/Broker 出站白名单，并将 Query 部署选择器统一为 `app=query-api-http`；`graph-networkpolicy.yaml:30-97` 补齐 HugeGraph/schema migrator 出站链路。Kubernetes API 不再伪装成 `kube-system` Pod，改为发布时注入 `kubernetesApiCIDRs`，缺失时 Helm fail-closed。架构契约、Helm 渲染 YAML 解析、部署契约和完整 workflow gate 均通过；生产 CNI 连通性仍未验证。
- **启动依赖与本地验证脚本：** `templates/ai-orchestrator/deployment.yaml` 和 `templates/investigation-worker/deployment.yaml` 增加 `wait-for-query-api` initContainer，以同一 TLS CA/Query `/readyz` 作为启动前置；`test-production-architecture-contracts.sh` 增加 ARCH-312 契约；`local-validation.sh` 在 `SKIP_IMAGE_BUILD=1` 时强制显式 `RELEASE_TAG`，并支持 `AIOPS_REUSE_K8S_TLS_SECRET` 避免验证期间无意轮换 CA。revision 7 本机两个 Worker 与 Gateway 的 initContainer 成功、业务容器重启数为 0。
- **生产 Mock fail-closed：** `ai-orchestrator/main.py` 在 `AIOPS_ENV=production`（或非本地的生产部署模式）且 `LLM_MOCK=true` 时在应用初始化前退出；`tests/test_llm_mock.py` 的子进程回归测试和 ARCH-404 生产渲染契约通过，避免运行时误把模拟诊断当作真实模型结果。

### P0：当前未确认 P0 级代码缺陷

未发现已由代码和本机证据共同证明的越权、跨租户写入、不可逆数据破坏或核心服务必现不可用。以下 P1 项仍足以阻断生产发布。

### P1-01：发布证据不可发布，代码/镜像/部署不可复核

- **类型/要求：** 发布流程缺陷；release manifest 必须绑定 commit、镜像 digest、rendered manifest、迁移/policy/data digest。
- **证据：** `collect-release-evidence.sh` 当前输出 `working_tree_dirty=true,publishable=false`；OrbStack revision 7 仍使用本地 `git-4036b46cf2e2` 镜像标签，且运行代码提交为 `2e02509`，尚未生成 registry immutable digest evidence。
- **触发/影响：** 将本机测试结果直接当生产候选，生产运行版本可能与报告代码不同，无法审计或安全回滚。
- **根因：** 工作区未提交，未执行 registry digest 构建/签名和候选环境部署。
- **整改实现：** 提交当前修复；构建所有自研镜像并记录 digest；`helm template` 固定 values/Secret 引用；在隔离 namespace 部署；采集测试、Pod digest、migration checksum、Graph/Provider/rollback 结果。
- **验收标准：** evidence JSON 的 `git_commit` 与源码提交一致、所有 Pod image digest 与 manifest 一致、`publishable=true`；回滚到上一 digest 和再次升级均成功；工作区无审查工具生成的未跟踪 runtime artifact。

### P1-02：生产 Secret、证书身份和轮换证据缺失

- **类型/要求：** 配置/安全发布阻断；生产不能使用占位 Secret，内部服务需可验证 mTLS 身份。
- **证据：** `deploy/helm/aiops/values-prod.yaml:18-24,100-109` 明确要求 release 系统注入 `clientSAN`、Secret 和 admin bootstrap，默认 `CHANGE_ME`/空值会被拒绝；`templates/_helpers.tpl:12-24,174-181` 在 mTLS required 时强制渲染 `AIOPS_TLS_CLIENT_SAN`；revision 7 的 9 个 Deployment 均注入非空 allowlist，`bootstrap/mtls.go:28-88` 及各 Go 服务 `mtls.go` 执行 SAN 校验。本机已验证无客户端证书内部请求 401，但未验证错误 SAN/过期/轮换；Python uvicorn 仍只有 CA/client-cert 校验，缺少逐服务 SAN allowlist。
- **触发/影响：** 直接部署 prod values 会 fail-closed；若用共享 CA 但不限制 SAN，任意受信客户端证书可能扩大服务身份边界；无轮换演练会导致升级中断。
- **根因：** Secret manager/cert-manager 的生产材料不在仓库；本轮已将 SAN 变成 Helm required 配置并在 Go listener 强制校验，但生产证书粒度和 Python SAN enforcement 尚未冻结。
- **整改实现：** 采用 ExternalSecret/Vault/KMS；为每个服务分配证书或 SPIFFE URI SAN，将当前共享本地 allowlist 替换为 per-service `clientSAN`；为 Python uvicorn 增加等价 SAN 身份校验；保留 `/internal` client-cert enforcement；增加有效、无证书、错误 SAN、过期、轮换和回滚测试。
- **验收标准：** `helm template` 无明文/占位；删除或错误 Secret 时 readiness fail-closed；无客户端证书/错误 SAN 的内部请求 401/握手失败；有效证书+有效 JWS/nonce 才 2xx；轮换在不丢请求或按 runbook 可回滚；证书序列号/有效期只进脱敏 evidence。

### P1-03：RCA 观测数据源未达到可用证据门槛

- **类型/要求：** 配置/数据可靠性阻断；RCA 必须读取真实 metrics/logs/traces/alerts/changes，数据源不可用要显式失败而不是空成功。
- **证据：** revision 7 运行在此前真实 canary 的持久化存储之上；该 canary 通过 Ingest mTLS/API key 写入 1 条 OTLP 日志和 1 条 OTLP Trace，Query/VictoriaLogs 查询到 marker，Query/VictoriaMetrics 返回 1 次 call/error 聚合，Kubernetes Events marker 检查通过。`validate-observability-evidence.sh` 结果为 metrics/logs/events **PASS**，DeepFlow、service dependency、RCA **BLOCKED_BY_ENV**，总退出码 2；RCA Run 仍为 `partial`，因为完整根因门禁还要求跨域 alerts/changes/DeepFlow/依赖证据。不能把已有三类真实观测或 ClickHouse `SELECT 1` 当作完整 RCA 数据质量证明。
- **触发/影响：** 生产数据源认证或租户映射错误时，根因结果只能 partial；若 UI 忽略 quality，可能误导处置。
- **根因：** 本机已具备可审计的 metrics/logs/events canary，但 DeepFlow 被显式跳过，且没有完整 alerts/changes/依赖/RCA evidence URL 和真实 Provider；代码层 no-data envelope 已修复，候选环境仍未提供全域 datasource evidence。
- **整改实现：** 已在本机通过真实 Ingest→VM/VLogs→Query 链路验证 metrics/logs，并读取 K8s Events；下一步候选环境必须补 DeepFlow flow/span、alerts/changes、service dependency 和 RCA evidence URL，核对 `tenant_id/cluster_id`、migration checksum、reader mode；保留 `NO_DATA` 与 `BACKEND_UNAVAILABLE` 的不同告警，并以本轮 `internal_query.go:288-299,337-346` 的 complete envelope 语义作为回归基线。
- **验收标准：** 固定 tenant/cluster/time window 的 metrics/logs/traces/alerts/changes canary 全部返回 200 + 正确 quality；数据源故障返回明确 503/partial 且 UI 显示原因；RCA evidence count、provenance digest、tool error count 可在 MySQL/运行事件中关联；授权空数据必须是 `complete` 空结果，认证/连接故障必须保持 `BACKEND_UNAVAILABLE`，二者不可混淆。

### P1-04：真实 Graph 数据、负载和恢复未绑定候选版本

- **类型/要求：** 功能/容量/恢复门禁；HugeGraph 必须是可重建投影，schema/source/load/recovery/tenant isolation 证据需绑定候选 commit/digest。
- **证据：** `graph-networkpolicy.yaml:13-23` selector 已修为 `app=query-api-http`；Graph 合同脚本和 200k/1M dry-run shape check 通过；本机仅加载 2 vertices/1 edge，基础查询 200，未执行 200k/1M 实际负载、p95、恢复或跨节点演练。
- **触发/影响：** 图高扇出、重启或恢复后 RCA 查询超时/空图，根因排序和传播路径不可信。
- **根因：** 生产数据集、Graph 资源快照和恢复演练不在本机；代码已保守化默认边界，但小 fixture 不能替代容量证据。
- **整改实现：** 保持 `RCA_GRAPH_MAX_DEPTH=1,MAX_VERTICES=50,MAX_EDGES=150` 的安全默认；在候选环境按资源门禁逐级提升；记录 schema/data/source/recovery digest，建立 p95/timeout/error budget；Graph unavailable 时保持 `graph_partial/stale`，不推送动作。
- **验收标准：** 固定 dataset 下 cold start、增量 reconcile、断点恢复、租户隔离和查询 p95/503 阈值全部通过；同一候选 digest 的 Graph evidence 可重放；超限请求返回受控错误，不拖垮 Query/Worker。

### P1-05：Credential Broker/真实 mutation 链路只能作为受控能力，尚未生产验收

- **类型/要求：** 安全/功能阻断（仅当发布范围包含变更动作）；approved Action 必须经 Broker、短时 TokenRequest、TOCTOU/post-verify/audit。
- **证据：** Executor `main.go:445-497` 只接受 `credential_ref` 并向 Broker 请求；模板在 `executionMode=approved` 且 Broker 关闭时 fail；当前本机已恢复为 `disabled/realMutation=false`，生产 values 也默认 `credentialBroker.enabled=false`，没有真实 TokenRequest/audit evidence。
- **触发/影响：** 直接开放动作会不可用；绕过 chart 或复用 Pod SA 会违反最小权限并可能造成错误集群写入。
- **根因：** 生产 profile、Broker token、namespace/operation profiles、K8s API 和证书材料未提供；本轮只做禁写安全验证。
- **整改实现：** 先保持 disabled；需要动作时注入明确 profile、Broker mTLS/token、target namespace RBAC，执行器关闭 automount/fallback；为 valid/unknown ref、namespace/resource/action drift、过期/replay、broker down、响应丢失分别实现故障注入和 reconcile。
- **验收标准：** 未签名/跨 tenant/cluster/namespace/resource/action、未知 ref、过期/重放请求均拒绝；有效 profile 返回不超过 300 秒 TokenRequest；Action/Approval/Executor/Broker/K8s audit 可用 action_id/request_id 关联；Broker/Executor 不可用时不执行；`EXECUTION_MODE=disabled` 测试持续通过。

### P2-01：Legacy Chat/编排/兼容代码仍造成重复建设

- **类型/要求：** 架构债务；生产只保留 canonical Query→Worker/Orchestrator boundary，legacy 不能成为第二 owner/入口。
- **证据：** `main.py:1064-1207` 与 `1234-1356` 有两套 SSE thread/queue；`main.py:1584-1620`、`2342-2488` 等旧动作/审批路由仍存在但由 `LEGACY_DIRECT_MUTATIONS_ENABLED`/production gate 关闭；`investigation_app.py` 已不再 import main，说明运行时隔离已完成但源码仍冗余。
- **触发/影响：** 新功能若误接 legacy handler，会恢复 SQLite、shell 或旧 scope 依赖，造成行为分叉和安全回归。
- **根因：** 迁移采用环境开关和路由退休，未完成包级删除/编译隔离。
- **整改实现：** 将 canonical Chat/Report/Action 适配器移入独立模块；legacy 仅保留明确迁移 CLI；生产构建静态禁止旧 public route、SQLite owner、shell/K8s direct mutation；增加 route inventory contract。
- **验收标准：** production OpenAPI/路由清单无 legacy public handler；Worker import graph 不含 `main`/scheduler/SQLite；`rg` 静态规则和不可达测试通过；删除 legacy 模块后全量测试仍通过。

### P2-02：遗留 fixture/采集协议仍出现 scope 字符串（核心授权路径已关闭）

- **类型/要求：** 设计偏差/安全债务；ADR 要求零 header/default 回退，只有采集协议可保留固定 metadata。
- **证据：** `auth.go:317-366` 已删除 caller header/query/default 作用域来源；`handler.go:300-312` 未配置系统租户时不再回退固定 UUID；`main.py:1580-1625` legacy mutation 只使用签名 context。`ai-orchestrator/skills/vm_ops.py`、`multicluster_demo.py` 等非生产 fixture 仍出现 `default`/示例 scope；Collector/DeepFlow 的 `x-tenant-id` 属明确写入协议，生产架构契约 ARCH-105/106/107/108 已通过。
- **触发/影响：** 核心生产请求不会因客户端 header 或固定租户扩大 scope；若将遗留 fixture/兼容 helper 误打进生产构建，仍可能造成行为分叉和审计混淆。
- **根因：** 迁移期示例和兼容代码尚未完成包级删除；这属于架构债务，不是当前生产授权实现缺陷。
- **整改实现：** 保持公共/内部授权 fail-closed；所有 tool 强制 `ScopeView/TrustedRequestContext`；将 Collector/DeepFlow header 限制在协议适配层；继续删除或编译隔离遗留 fixture，并由 ARCH-108 及 route inventory 阻止回归。
- **验收标准：** 无 context/缺 scope/`default`/`all` 的生产授权请求均明确 4xx；跨 tenant/cluster 全拒绝；静态扫描仅允许协议适配目录出现 header；Query、Worker、UI 的租户/集群来源均为 MySQL/签名 context；遗留 fixture 不出现在生产镜像。

### P2-03：历史事件 event_id 迁移与 ClickHouse 合并未完成

- **类型/要求：** 数据一致性/迁移缺口；新旧 writer 不能同时写，历史重复必须可解释。
- **证据：** Collector 新路径生成 SHA-256 event_id，Ingest 强制 15 列，migration `0006_k8s_events_idempotency.sql` 更新排序键；但旧表/旧 writer 可能存在空 event_id，且本轮没有执行生产迁移或 `ReplacingMergeTree` merge 统计。
- **触发/影响：** 历史重放可能重复计数，RCA 时间线和告警去重失真。
- **根因：** 旧行没有可信统一身份，迁移脚本不能凭空推断 UID。
- **整改实现：** 用 K8s UID/SEL record id 有证据地回填；无法证明的行进入 quarantine；迁移窗口拒绝 14 列 writer；记录覆盖率、quarantine 数和 merge 后唯一计数。
- **验收标准：** 回填覆盖率和 quarantine 明确；同一 event_id 重放/重启 replay 后唯一计数不变；新旧 schema 混写被拒绝；迁移 checksum 与 release evidence 一致。

### P2-04：AICHAT 的真实 Provider、跨副本 resume 和并发首轮仍缺集成证据

- **类型/要求：** 功能/测试缺口；两个自研模块应在真实 Provider、断线、并发和降级下保持一致。
- **证据：** 本机 AICHAT 使用 `LLM_MOCK=true`；frontend/Go/Python 单测通过，且生产环境误启 Mock 已由 `main.py` 启动 guard 拒绝，但没有真实 Provider canary、跨 Worker/Query 副本恢复或并发 EnsureSession 测试。
- **触发/影响：** Provider 429/超时、网络断开或同一 session 并发请求时，可能出现重复消息、悬挂 SSE 或错误降级。
- **根因：** 真实 key/外部网络不在本次授权范围；Chat transcript 已迁移 MySQL，但负载/故障证据尚未补齐。
- **整改实现：** 见第 5.2 节：Proxy canary、heartbeat/deadline、session upsert、跨副本 resume 和脱敏日志。
- **验收标准：** 真实候选环境 200/SSE/done、Provider failure 状态、断线重连、并发 20 首轮和 token/key rotation 全部有机器可读 evidence；任何失败均显示明确原因，不伪造 assistant success。

### P2-05：状态组件与多副本 HA/备份恢复未验证

- **类型/要求：** 可靠性/运维缺口；RWO 数据、PDB、DB failover/PITR 必须有候选证据。
- **证据：** `values-prod.yaml:11-25` 明确 Orchestrator/Ingest 当前单副本；本机 PVC 为 local-path/RWO；Worker 2 副本 Ready，但跨节点、network partition、MySQL/ClickHouse/PVC 故障和 rollback 未演练。
- **触发/影响：** 节点故障或 PVC 不可用可能丢失 WAL、checkpoint、transcript 或阻塞升级。
- **根因：** 本机单节点 OrbStack 不代表生产拓扑，外部 StorageClass/backup/PITR 材料缺失。
- **整改实现：** 将 Query/Worker 无状态副本与外部化 MySQL/CH/WAL/Transcript 分离；执行备份、恢复、故障注入、PDB 和跨 AZ 演练。
- **验收标准：** RPO/RTO 达到 `docs/runtime-slo.md`；WAL/replay、MySQL PITR、CH/Graph 重建、Worker failover、版本回滚全部有时间戳和 digest 证据；未通过时发布门禁保持 blocked。

### P3-01：前端 bundle 和依赖弃用警告

- **类型/要求：** 一般质量改进，不构成当前安全阻断。
- **证据：** `npm run build` 报 GraphExplorer 约 1.4MB、多个 chunk 超过 500kB；测试报告 React Router future flag 和 Antd `destroyOnClose` 弃用。
- **整改/验收：** 路由级 dynamic import/manualChunks，升级 Antd API，设置 chunk budget；构建告警归零或由有期限的例外记录明确豁免。

## 7. 架构专项评价

| 专项 | 结论 | 评价 |
|---|---|---|
| 服务拆分 | 有限通过 | Query HTTP/Run/Alert/Worker/Collector/Ingest/Proxy/Broker/Executor 边界清晰；Orchestrator gateway 仍包含 legacy 兼容职责。 |
| 分层与依赖方向 | 有限通过 | Worker/Orchestrator 通过 Query internal client 读取事实，Collector→Ingest 已统一；legacy Python import graph 仍可能扩大依赖。 |
| 接口契约 | 有限通过 | Go/Python/TS 的 UUID、Run window、target_type、ToolResultEnvelope、SSE 和 signed context 已对齐；旧路由数量多，需 route inventory。 |
| 数据 owner/事务 | 有限通过 | MySQL owner、outbox/lease/event/evidence/action 事务和 Chat scope 已具备；历史 CH migration、真实 datasource 和 backup 未验收。 |
| 安全权限 | 有限通过 | JWT role 不授权、MySQL SoT、capability/scope/replay、credential_ref、禁写默认已实现；mTLS SAN/轮换及生产 Secret 缺证据。 |
| 可靠性 | 有限通过 | WAL、outbox、lease、bounded graph、timeouts、PDB、readiness 存在；跨副本 replay、HA/PITR、Provider fault injection 未完成。 |
| 性能扩展 | 部分通过 | Query/Worker 可横向扩展；Graph 高扇出、Ingest 单写 PVC、Python SSE/LLM 资源和前端大 chunk 仍需预算。 |
| 可观测性/审计 | 部分通过 | request/run/session/tool/action/event ID、metrics、health、evidence JSON 已有；Datasource error 和证书/回滚/HA evidence 尚未集成 release gate。 |
| 部署运维 | 不通过 | Helm 合同和禁写 fail-closed 通过，但生产 Secret、证书、StorageClass、镜像 digest、迁移/恢复/rollback 尚未齐全。 |
| 可测试性 | 有限通过 | Python/Go race、前端和合同覆盖高；真实 CH/Graph/Provider/K8s TokenRequest/mTLS/多节点集成缺口明确。 |

## 8. 整改路线（依赖顺序与验收门槛）

1. **R0 发布安全冻结（立即）**：保持 Executor `disabled/realMutation=false`，禁止生产动作；清理测试产生的 runtime artifact；提交当前代码并生成 digest/evidence。门槛：`publishable=true` 前禁止生产发布。
2. **R1 身份与证书**：接入 ExternalSecret/cert-manager 或 SPIFFE；为 service SAN、CA、轮换和 client transport 固化配置；执行证书拒绝矩阵和跨副本 nonce。门槛：无证书/错误 SAN/过期/重放均失败，有效请求可关联审计。
3. **R2 数据源与迁移**：核对 Query→CH/VM/VLogs/K8s 的凭据、schema、租户映射；执行 0005–0007/历史 event_id 受控迁移和 quarantine；验证 CH 去重。门槛：固定 canary 全部 quality 正确，RCA 无 backend unavailable。
4. **R3 Graph 容量与恢复**：用候选 digest 执行 schema/source/load/reconcile/recovery/tenant isolation；从深度 1/50/150 开始按 p95/资源预算提升。门槛：Graph evidence 与 commit/dataset/recovery digest 一致，超限受控拒绝。
5. **R4 AICHAT 生产化**：Proxy provider canary、SSE heartbeat/resume、session upsert/并发、真实错误映射、脱敏审计。门槛：真实 Provider 与故障矩阵通过；不能把 mock 结果当生产通过。
6. **R5 受控动作（按需）**：仅在动作产品范围明确后启用 Broker profiles、TokenRequest、最小 RBAC、TOCTOU/post-verify/reconcile/audit；否则保持 disabled。门槛：所有 scope/profile/replay/broker-down 测试通过。
7. **R6 HA/运维**：MySQL PITR、CH/Graph 重建、WAL/Worker/Query failover、NetworkPolicy、PDB、升级/回滚和 SLO 证据。门槛：RPO/RTO、故障注入、回滚和当前 digest 全部写入 release manifest。
8. **R7 清理债务**：删除 legacy Chat/SQLite/scope fallback、统一 SSE adapter、前端 chunk/deprecation；门槛：生产路由/依赖静态 contract 无旧 owner，前端 chunk budget 通过。

## 9. 生产发布门禁

### 已通过（代码或本机证据）

- Query/Dispatcher/Alert/Worker 的真实 Deployment 和调用边界；Worker 不再 import `main`；Tool Registry 和 evidence provider 初始化问题已修复。
- MySQL IAM/session/scope、HttpOnly Cookie、JWT role 不授权、canonical cluster UUID、Run window/target_type、Chat scope ownership。
- TrustedRequestContext、capability、nonce/replay、ToolResultEnvelope、RCA entity/provenance/partial 输出；无签名 internal graph 请求返回 401。
- `NO_DATA` ToolRun 持久化语义已修复并有 Go 回归测试；本机真实 RCA Run 的 8/8 工具为 `success/complete`、6 条证据。
- mTLS required/SAN 配置已进入 Helm revision 7；9 个服务注入 SAN，Query 无客户端证书内部请求返回 401。
- Collector→Ingest WAL/15 列/event_id、ClickHouse migrations/contract；Graph NetworkPolicy selector 修复；RCA bounded candidate limits。
- 完整 workflow gate、Helm 和合同脚本均通过（Python 1203 tests、前端 39 tests/build）；本轮 Go 服务逐包测试也通过。
- 本机 Helm revision 7 所有服务 Ready；9 个内部服务实际注入 `AIOPS_TLS_CLIENT_SAN`；Gateway/Worker `wait-for-query-api` initContainer 成功且业务容器重启数为 0；运行态 Query NetworkPolicy 选择器已核对为 `app=query-api-http`；Action Executor 保持 `disabled/realMutation=false`，未调用任何 mutation endpoint。

### 未通过（明确阻断）

- P1-01：工作区未提交、release evidence `publishable=false`；
- P1-02：生产 Secret、逐服务证书身份/SAN、错误证书拒绝、轮换和撤销材料未在候选环境验收；
- P1-03：真实 metrics/logs/events 已在本机通过 Ingest→VictoriaMetrics/VictoriaLogs/Query canary，validator 对三项返回 PASS；DeepFlow、service dependency、RCA evidence 仍 `BLOCKED_BY_ENV`，因此总门禁仍 exit 2；本机 Run 的 8/8 ToolRun `success/complete` 不能替代缺失的跨域证据；
- P1-04：Graph 真实 dataset、容量和 recovery evidence 未绑定候选 digest；
- P1-05：真实 mutation 若属于发布范围，Broker/TokenRequest/审计尚未验收；
- P2-03：至少一次事件语义的历史 event_id 迁移未执行；若发布要求历史数据一致性，必须纳入门禁。

### 未验证（必须补充环境证据）

- 生产 mTLS client SAN、证书轮换/撤销、跨副本 replay；
- 生产 MySQL/ClickHouse/HugeGraph migration、merge、备份/PITR/恢复；
- 真实 LLM Provider canary、429/5xx/timeout、限流/熔断、key rotation；
- Kubernetes TokenRequest、Broker profile、Action post-verify/reconcile、K8s audit；
- 多节点/多 AZ、NetworkPolicy/CNI、PDB、StorageClass、升级/回滚和 RPO/RTO；
- 当前候选镜像的完整 rendered manifest、immutable digest、Graph/data/policy/migration evidence。

**发布判定：不允许发布。**

若产品只发布只读 AIOps/AICHAT，不开放 mutation，最小解除集合是 **P1-01、P1-02、P1-03、P1-04 + P2-03（历史数据被纳入发布范围时）**，并完成 HA/备份门禁。若包含变更动作，再加 **P1-05**。任何一项缺证据只能保持“未验证/阻断”，不能用单元测试或 Helm lint 代替。

## 10. 未决事项与证据索引

### 10.1 仍需外部材料的事项

1. ExternalSecret/Vault/KMS 配置、证书 CA/SAN/轮换记录和渲染后脱敏 manifest；
2. 候选镜像 registry digest、signed release manifest、迁移/policy/Graph dataset checksum 和 rollback 结果；
3. Query→ClickHouse/VM/VLogs 的真实凭据绑定、表版本、tenant/cluster 数据抽样和故障演练；
4. Graph schema/source/recovery/load 版本、节点/边计数、p95/503/资源快照、租户隔离；
5. Provider profile/canary、Broker profile/TokenRequest、动作审批/执行/回写/K8s audit；
6. 多副本 replay/nonce、MySQL PITR、WAL/Worker/Query failover 和 RPO/RTO 报告。

### 10.2 代码与配置证据索引

- 架构/所有权：`README.md`、`docs/architecture/`、`docs/ownership/data-owners.md`、`docs/runtime-slo.md`；
- 身份/授权：`ai-apm-query-go/internal/api/auth.go`、`internal_query_envelope.go`、`store/authorization.go`、`ai-orchestrator/trusted_context_issuer.py`、`internal_ingress.py`；
- AICHAT：`ai-apm-query-go/internal/api/settings.go`、`internal/api/ai_chat_sessions.go`、`internal/store/ai_chat_sessions.go`、`observability-frontend/src/pages/ai/AiChat.tsx`、`ai-orchestrator/main.py`；
- Worker/RCA：`ai-orchestrator/apps/investigation.py`、`investigation_app.py`、`rca_engine/{engine.py,runtime.py,candidates.py,entity_resolver.py,contradictions.py}`；
- 采集/数据：`ai-event-collector/{clickhouse.go,wal.go}`、`ai-apm-ingest-go/cmd/ingest/{main.go,event_wal.go}`、ClickHouse migrations `0005–0007`；
- 动作/凭据：`ai-action-executor/main.go`、`ai-credential-broker/main.go`、`ai-orchestrator/{credential_broker.py,execution_adapter.py}`；
- mTLS/部署：各服务 `mtls.go`、`ai-apm-query-go/internal/bootstrap/mtls_test.go`、`ai-orchestrator/mtls.py`、`deploy/helm/aiops/templates/`、`values-prod.yaml`、`values-local-validation.yaml`；
- 验证脚本：`deploy/scripts/collect-release-evidence.sh`、`test-production-architecture-contracts.sh`、local/deployment/Graph/Observability contract scripts。

本报告已删除旧版“mTLS 未实现”“Worker 仍导入 main”“本机运行旧镜像”“NO_DATA 必然导致 ToolRun failed”“Query 仍信任 caller X-Tenant-ID”等与当前代码/本机 revision 4 不一致的结论；同时保留了真实未验证项和导致生产阻断的最小问题集合。
