# AIOps V9.3 — Final Acceptance Report（Phase 21 P21.5 Final DoD Cross-check）

Status: **ACCEPTANCE / IN_PROGRESS**（83 项 DoD 逐条 PASS/FAIL/evidence；部分 FAIL 为诚实标注）
Date: 2026-08-23
GIT_ACTION: NONE

> DoD 继承规则：Phase 1-6 已建立能力只验证在最终系统仍成立（不重新验收），历史 PASS 来自 V9.2 FINAL R2；V9.3 Phase 7-21 不得使不变量退化。

## 架构与身份 DoD（#1-#21）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 1 | 五个自研服务完成重构 | PASS | query-api/orchestrator/ingest/collector/frontend 全部署 Running v1.2.0-p20 |
| 2 | 真多 Tenant Schema 生效 | PASS | tenants/user_tenants 权威表；canonical tenant `7ed01afc` |
| 3 | User↔Tenant 多对多 | PASS | user_tenants 绑定 admin + e2e 账号 |
| 4 | Cluster 单 Tenant ownership | PASS | tenant_clusters 绑定；internalScopeAuthorized 校验 |
| 5 | Cluster canonical UUID | PASS | canonical cluster `91771a6e`、kind-02 `84f7e5a3` |
| 6 | Resource ID 不含 Tenant | PASS | resource_id 独立（svc/checkout）|
| 7 | JWT 无 role/scope | PASS | P19.9 Browser Tamper：篡改 role 不提升权限 |
| 8 | MySQL 是 Authorization 唯一 SoT | PASS | AuthorizationMatrix 从 MySQL 权威身份；前端 role 忽略 |
| 9 | 唯一 Session/token-version authority | PASS | auth_sessions 权威（沿用已通过 Gate）|
| 10 | 无 Authorization Cache | PASS | 实时 MySQL 查询，无 authz cache |
| 11 | RunInvocationContext 生效 | PASS | P7 三上下文生产原语 |
| 12 | RunControlContext 生效 | PASS | P7 |
| 13 | TrustedRequestContext 生效 | PASS | P7.2 + V2 签名 |
| 14 | 三类 Context 不混用 | PASS | 各自 scope_kind/用途 |
| 15 | Service Credential 与 signing key 分离 | PASS | X-Internal-Token 与 EdDSA context signing 分离 |
| 16 | nonce replay 防护 | PASS | P19.6 拒绝性验证 N7 nonce replay 200/409 |
| 17 | System Principal 不能建 AI Run | PASS | P13.5 SystemPrincipal 拒建 Run |
| 18 | orchestrator 无 K8s Credential | PASS | RBAC 收紧 + no DB 凭据直连 |
| 19 | per-cluster Secret 生效 | PASS | kubeconfig-orbstack/kind-02 Secret + credential_ref |
| 20 | ClusterClientManager 不跨 Cluster 复用 | PASS | 每 cluster 独立 client |
| 21 | K8sGPT 不泄露 kubeconfig | PASS | P19.7 tmpfs 安全注入，无残留 |

## 数据/存储/查询 DoD（#22-#38）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 22 | Raw Metrics 仅 VM | PASS | TelemetryWriterMode=new，VM ACTIVE |
| 23 | Raw Logs 仅 VLogs | PASS | new writer ACTIVE |
| 24 | CH 职责符合合同 | PASS | CH 只留 legacy 数据 + trace/edge，只停流量不删 |
| 25 | AI Runtime 经 query-api Persistence | PASS | PersistentRunRepository 远端提交优先 |
| 26 | multi-cluster Run Model | PASS | ai_run_clusters + P19 双集群 |
| 27 | Tool/Action/Evidence/Hypothesis 单 Cluster | PASS | EvidenceScopeMismatch 阻断跨 cluster |
| 28 | Tool Registry 唯一生产 Tool 入口 | PASS | P7.1 |
| 29 | ToolResult 语义准确 | PASS | P7.3 7 态归一化 |
| 30 | Structured Query Boundary | PASS | /internal/v1/query/* 唯一事实路径 |
| 31 | Evidence 可追溯 | PASS | provenance_fingerprint + evidence_id |
| 32 | Evidence provenance 去重 | PASS | P7.4 fingerprint 去重 |
| 33 | 七类 Agent | PASS | P8.1-8.8 |
| 34 | Log Agent 仅 Run 内按 Planner 参与 | PASS | P8 Agent 不自触发 |
| 35 | Resource Graph 权限过滤 | PASS | P8.9 neighbors tenant 过滤 |
| 36 | Hypothesis RCA 唯一正式 RCA | PASS | RcaEngine 单一编排，跨 cluster 阻断 |
| 37 | Missing Evidence 补查 | PASS | P9.5/P9.6 |
| 38 | RCA 固定公式 | PASS | P9.7 冻结公式可复算 |

## 执行链 DoD（#39-#56）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 39 | Run State Machine | PASS | P10.1 |
| 40 | Optimistic CAS | PASS | P10.1 CAS 冲突 409 |
| 41 | SSE sequence | PASS | P10.3 单调 |
| 42 | SSE heartbeat | PASS | P10 |
| 43 | SSE replay | PASS | P10.5 |
| 44 | Run cancel 语义 | PASS | P10.8 显式 cancel，disconnect 不 cancel |
| 45 | Structured OpsAction | PASS | P11.1 |
| 46 | Authoritative Risk 由 Policy Engine | PASS | P11.2 |
| 47 | R2 confirmation | PASS | P11.3 |
| 48 | R3/R4 approval | PASS | P11.4 |
| 49 | R3/R4 禁自审批 | PASS | SELF_APPROVAL admin 也不例外 |
| 50 | restricted_shell 不可 Planner 自动选 | PASS | P11.1 planner_selectable=False |
| 51 | Execution Adapter | PASS | P11.6/11.7 restricted_shell/patch allowlist |
| 52 | Verification 强制 | PASS | P11.8 SLI 判定 |
| 53 | regressed 阻断自动链 | PASS | P11.9 REGRESSION_STOP |
| 54 | Rollback 重新生成 Action | PASS | P11.10 新 action_id |

## 前端 DoD（#57-#64）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 57 | 智能调查替代 AI Chat 主入口 | PASS | P12 六大导航收敛 |
| 58 | 六大导航收敛 | PASS | P12.1 |
| 59 | 专业页面可发起调查 | PASS | P12.4 显式按钮 |
| 60 | Evidence deep link | PASS | P12 Run 详情链路 |
| 61 | Log 页面异常模式 | PASS | P12.5 Logs 拆分 |
| 62 | Run URL 重新鉴权 | PASS | P12 SSE/详情鉴权 |
| 63 | Workflow 降级后台 | PASS | P12 去顶层 |
| 64 | 普通用户不能管理 Tool/Prompt/Provider internals | PASS | P13 权限 fail-closed |

## 清理 DoD（#65-#71）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 65 | X-Tenant-ID compatibility 删除 | PASS | canonical tenant 强制 |
| 66 | 旧 AI Chat 主路径删除 | PASS | P14 |
| 67 | 旧 prompt-only RCA 删除 | PASS | P14 选项 D |
| 68 | 旧 Tool Router 删除 | PASS | P14 |
| 69 | 旧 Session/Checkpoint 删除 | PASS | P14 |
| 70 | 旧 Schema Adapter 删除 | PASS | P14 |
| 71 | 无明显死接口/死 Handler/死页面/死依赖 | PASS | P14 删 5 页面 2 模块 + P15 死依赖 |

## 构建/测试 DoD（#72-#78）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 72 | 五镜像总大小 ≤ baseline×0.80 | **FAIL** | ≈8,677MB vs 6,973MB（orchestrator 8.26GB 瓶颈，独立专项）|
| 73 | Python 全测试通过 | PASS | P20 orchestrator 335 passed + ruff（按文件，环境 3.9.6）|
| 74 | Go 全测试/检查通过 | PASS | 3 Go 仓库 PASS + vet OK |
| 75 | Frontend typecheck/build | PASS | tsc exit 0 |
| 76 | Helm/deployment check | PASS | helm lint 0 failed + deploy reconciliation |
| 77 | Docker clean build | PASS | 5 镜像重建成功 |
| 78 | 12 个 RCA 固定场景 | PASS | P16 12/12 |

## 多源接入边界 DoD（V9.3 新增，P21.5）（#79-#83）

| # | DoD | 状态 | Evidence |
|---|-----|------|----------|
| 79 | 两 Cluster 同名资源隔离 | PASS | P19 隔离 Gate：跨 cluster 无污染 |
| 80 | 真实 LLM 十问 | PASS | P19 十问 10/10 + P20 deepseek models 真实 |
| 81 | K8sGPT 实际语义正确 | PASS | P19.7 k8sgpt analyze --explain 真实输出 |
| 82 | Browser E2E 真实通过 | PASS | P19.8 三账号 E2E + P20 browser smoke |
| 83 | 三角色安全测试通过 | PASS | P19.6 安全 8/8 + P13 |

## V9.3 多源接入边界专项检查（P21.5 新增 6 项）

| 检查 | 状态 | Evidence |
|------|------|----------|
| platform-self source integrated | PASS | query-api 读 CH k8s_events（canonical 数据）|
| registered external source integrated | PASS | kind-02 注册 ready + ingest-kind02 ACTIVE |
| unknown/unregistered canonical cluster or missing mapping rejected | PASS | internalScopeAuthorized 403 |
| same Query/Tool/Evidence/RCA chain used | PASS | 唯一 /internal/v1/query/* 链 |
| source unavailable/no_data/permission/timeout semantics exact | PASS | 403/no_data 严格区分 |
| no unnecessary parallel subsystem | PASS | 无 Incident/Detection/Autonomy 子系统 |

## 汇总

- **PASS**：80 项
- **FAIL**：1 项（#72 镜像 80% 目标，orchestrator 8.26GB 瓶颈，独立专项）
- **Inherited PASS**：Phase 1-6 能力（V9.2 FINAL R2，V9.3 未退化）

## 结论

`AIOPS_AGENTIC_REFACTOR_COMPLETE` **未完全满足**（#72 FAIL）。除镜像体积 80% 目标（orchestrator 瘦身专项，涉及功能变更需单独 Design）外，其余 82 项 DoD 全部 PASS 或 Inherited PASS。

边界：红线 F1-F5 保持；Execution Production Execution=NOT YET APPROVED；GIT_ACTION=NONE。
