# AIOps V9.3 — Implementation Report（Phase 21 P21.2）

Status: **IMPLEMENTED / REPORT**（逐 Phase Gate evidence + source identity）
Date: 2026-08-23
GIT_ACTION: NONE

> 本报告是逐 Phase 实现汇总。详细 Evidence 见 `docs/V9.2_V9.3_P0_P9_IMPLEMENTATION_EVIDENCE.md`（单一主 Evidence）。**不写最终 commit SHA**（当前禁止 commit）。

## Source Identity

```text
source_tree_hash = 24b157a08a02f6b469dffa3bdc0008264c2f72cdbb95f0adf0abb32361d3b866
build_id         = 24b157a0
version          = v1.2.0-p20-24b157a0
```

## Phases

### V9.2 Phase 1-6（基线）
- **P1 Gate1 PASS**：冻结基线。
- **P2 Gate2 PASS**：冻结架构/所有权/契约。
- **P3 Gate3 PASS**：P0 defect=0；orchestrator 403 passed；多 tenant/cluster 信任边界（EdDSA 签名/MySQL control-plane schema/三 context 生产原语/Cluster Registry/ResourceResolver）。
- **P4 Gate4 PASS**：新数据模型 + migrator 框架。
- **P5 Gate5 PASS**：writer 实现 + atomic-cutover readiness。
- **P6 Gate6 PASS**：atomic cutover（canonical tenant `7ed01afc`/cluster `91771a6e`/pubkey `fQmL5`；legacy writer/reader ABSENT，new ACTIVE）。

### V9.3 Phase 7-11（可信分析 + 执行）
- **P7 Gate7 PASS**：可信分析 10 组件（Tool Registry/InternalQueryClient/TrustedContextIssuer/ToolResult/Evidence Hub/IntentEngine/Planner/InvestigationState/ManualBoundary/DataSourceMapping），163 passed。
- **P8 Gate8 PASS**：七类 Agent + Resource Graph，187 passed。
- **P9 Gate9 PASS**：Hypothesis RCA（RcaEngine 单一编排，109 passed）；Gate9 重做 PASS。
- **P10 Gate10 PASS**：Run Persistence/SSE/Recovery（PersistentRunRepository fail-closed；真实 MySQL 集成 PASS）。
- **P11 Gate11 PASS**：Remediation/Approval/Execution（approval 接权威 SoT，verification 用 SLI）。

### V9.3 Phase 12-20（收敛/部署/真实环境）
- **P12**：前端产品收敛（六大导航 + 调查中心 + 移除 DEMO）。
- **P13**：服务端安全加固（AuthorizationMatrix fail-closed，17 tests）。
- **P14**：删旧（Legacy Removal，选项 D 依赖倒置）。
- **P15**：依赖与镜像精简（4 Go 镜像 -trimpath/-s -w；前端移除 7 死依赖；orchestrator 8.25GB 未瘦身）。
- **P16**：全量自动化测试 + 12 固定 RCA 场景。
- **P17**：数据重置（P17.1-P17.6，备份 + TRUNCATE，PRESERVE 保留）。
- **P18**：构建与部署（5 镜像，digest reconciliation）。
- **P19**：真实环境 + 多集群（LLM 设置修复、K8sGPT 安全注入、双集群接入、隔离 Gate）。
- **P20**：缺陷收口 + 最终构建（Plan 1-4，v1.2.0-p20-24b157a0，Gate 9-13 重做 + Fresh Final Cycle）。

## Deviations（偏离）

- **P15 orchestrator 镜像瘦身未达成 80% 目标**（FINAL_TOTAL=8,694MB vs 80% 目标 6,973MB，-0.25%）：根因 ai-orchestrator 8.25GB 占 94.7%（torch/chromadb/sentence-transformers 运行时硬依赖）。拆为独立专项，不阻塞 P15 完成。
- **P14 旧 RCA 端点保留**（full_rca_analysis/node_rca）：需真实 query-api 数据构造 Evidence，属真实环境 Integration Gate，保留为生产主链。
- **P12 真实主旅程 UI 测试**受 AUTH blocker（前端 default tenant→query-api 400）阻塞，后在 P19 通过浏览器 E2E 解决。
- **本机 Python 3.9.6** 全量 pytest 收集 flow_engine 报既有 error（需 3.10），测试按文件运行。

## Blocked Items（阻塞项）

- **Execution Production Execution = NOT YET APPROVED**：真实业务执行（approval→execution→真实 K8s/OpenStack 变更）需 Execution Production Enablement Gate 单独批准 + 真实基础设施。红线 F5 保持。
- **egress default-deny 渐进启用暂缓**（P20-BUGBOT-P0-04）：集群不稳定时暂缓，RBAC（更关键 P0）已收紧。
- **two-cluster isolation 为外部依赖**：kind-02 已接入验证（P19 B v0.2），跨集群写仍受控。

## Resolved（已解决关键项）

- **AUTH blocker**：前端 default tenant → canonical tenant 修复（P19）。
- **K8s 数据 403**：边界扩展暴露 pods + orchestrator 走内部边界 + 长寿命 SA token（P19.9）。
- **LLM 设置页**：legacy-route fence + ModelsLLM panic 修复（P19.6）。
- **迁移 bug**：0003 重复列导致假成功 → 修复只 ADD 缺失列（P10 真实环境核验）。
- **RBAC 未收紧**：orchestrator 集群写权限撤销（P20 Plan 2）。

## 测试基线（Phase 20 本轮窗口）

```text
Go（query-go/ingest/collector）: 全 PASS + vet OK
orchestrator: 335 passed（核心子集）+ ruff 干净
frontend: tsc exit 0
P9: 111 passed / P10: 31 passed + 真实 MySQL / P11: 30 passed
P13: 42 passed / Gate 9-13 重做: 全部 PASS
```

## 边界

- 红线 F1-F5 保持（Gate 6 cutover 已验收）；Execution Production Execution=NOT YET APPROVED。
- GIT_ACTION=NONE；Phase 21 后 `WAITING_USER_AUTHORIZATION_FOR_GIT_COMMIT_AND_PUSH`。
