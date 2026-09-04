# AIOps 后续治理任务清单（BACKLOG）

> 更新：2026-09-04。主整改（P1 全部 / Phase 2-4 本地可验部分）已完成并随 main 交付；下列为**已明确记录但未实现/待外部输入**的治理项。状态以最终报告 §4 总表与 `docs/remediation/2026-09-03/phase*/…_DELIVERY.md` 为准。

## A. 需外部输入/环境才能推进（blocked）

| 项 | 目标 | 阻塞 | 记录位置 |
|---|---|---|---|
| P2-HA1 Orchestrator 单副本 | checkpoint 外部化 + worker lease/fencing + ≥2 副本，或业务明确 SLO 后收口 | 业务正式 SLO | 报告 §21、`docs/runtime-slo.md` |
| 真实 LLM 链路 E2E（RCA/Action/Approval/Reconcile） | 正式候选触发真实 run 验证 | 真实 provider key（候选 llmMock=true） | `phase4/PHASE4_DELIVERY.md` §6 |
| 节点驱逐恢复测试 | kill node → workload 迁移恢复 | 需 ≥2 节点候选 | 同上 |
| `publishable=true` 正式判定 | 生成生产背书 evidence | 生产 registry + 生产签名 key + 真实 secret 注入（机制已 drill 验证） | `deploy/evidence/release-candidate-manifest.json` |
| 生产 registry 镜像推进与签名 | 镜像进生产仓库 + cosign/等价签名 | 发布系统/凭据 | `docs/DEPLOYMENT_AND_VERIFY.md` §2.3/§4.2 |

## B. 代码/工程治理任务（未排期）

| 项 | 范围 | 来源 |
|---|---|---|
| P2-SEC1 剩余专项 | 其余组件（query-api/ingest/executor/egress/frontend）Dockerfile USER 非 root；orchestrator `readOnlyRootFilesystem` 化（写路径 emptyDir/PVC 化） | `phase2/PHASE2_DELIVERY.md` 诚实边界 |
| P2-A2 逐 API 显式 scope | 前端将 `GLOBAL_PATHS` 过滤语义重构为逐 API scope 声明（interceptor 内存化已完成） | 同上 |
| migration 覆盖测试接入 CI | `TestMigratedSchemaCoversLegacyEnsureSchema` 为 P4.9 gate 专用；建议并入 CI workflow-contract（真 MySQL）防回退 | `ai-apm-query-go/.../migrations/coverage_test.go` |
| main 证书/secret 轮换自动化 | 候选环境 mTLS 30 天证书到期提醒/自动轮换（2 天版已改 30 天） | `deploy/scripts/local-validation.sh` |

## C. 一致性/可观测（可选）

- 报告正文 §5-§21 各小节历史状态措辞保留以追溯，当前状态以 §4 总表（2026-09-04 已同步）为准。
- `deploy/evidence/release-evidence.json` 入库版本绑定发布时 commit；后续每次发布前按 P1-REL1 语义重跑 `collect-release-evidence.sh` 再绑定。
