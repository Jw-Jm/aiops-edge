# P1-SUP2 交付记录：production 镜像 digest pin（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`。依据审核报告 §9。

## 1. 修改文件清单

| 文件 | 变更 |
|---|---|
| `deploy/helm/aiops/templates/_helpers.tpl` | `aiops.imageWithGlobalTag` 支持 `digest`/`env`：production 缺 digest → **fail**；digest 格式错 → fail；digest 优先于 tag（剥离 repo 中的 stale :tag 拼 `@sha256`）；non-production 允许 tag 模式 |
| `deploy/helm/aiops/values.yaml` | `global.imageDigests`：13 组件键（默认空 → production 裸渲染即 fail，杜绝静默 mutable tag 部署） |
| 12 个模板调用点（13 处） | 注入 `digest`（来自 `global.imageDigests.<component>`）+ `env` |
| `deploy/scripts/verify-aiops-workflow-gates.sh` | production render 注入 13 个假 digest；新增 sup2 断言：digest 引用 ≥13 且自研镜像零 mutable tag |
| `deploy/scripts/test-deployment-contracts.sh` | bootstrap 渲染补 `--set global.environment=local`（contract 语义属 local） |
| `deploy/scripts/test-image-digest-contracts.sh` | **新增**：§9.3 五项 Helm contract 测试，接入 gate contracts 段 |

## 2. 修复内容

- **helper 规则**（§9.2）：`environment=production` 时每个自研镜像必须 `repository@sha256:<64hex>`；tag-only / 缺 digest / 格式错 / `@` 混合 → 全部 `helm template` fail。`global.imageTag` 不再是 production 的最终 identity（仅 local 有效）。
- **13 个自研组件**覆盖：query-api/ingest/event-collector/ai-orchestrator/investigation-worker/frontend/ai-action-executor/credential-broker/llm-egress-proxy/ipmi-exporter/clickhouse-migrator/schema-migrator/graph-schema-migrator。
- 第三方镜像（mysql/clickhouse/victoria/hugegraph 等）不受影响。

## 3. 验证（§9.3 五项 + 实际渲染）

```bash
# T1/T2 production 无 digest / tag-only      -> helm template FAIL ✓
helm template aiops . -f values-prod.yaml    # "production requires an immutable digest for self-owned image query-api:v1.1.1 ..."
# T3 digest 格式错                            -> FAIL ✓
# T4 注入 13 digest 渲染                      -> PASS，9+ 组件全 @sha256，零 mutable tag ✓
# T5 local 环境                              -> `query-api:v9-test` tag 模式保留 ✓
AIOPS_GATE_STAGES=helm,contracts bash deploy/scripts/verify-aiops-workflow-gates.sh  # exit=0 全绿
```

## 4. 关闭结论：CLOSED

- ✅ `helm template -f values-prod.yaml`（注入 digest）渲染：所有自研 workload 最终 image 均 `@sha256:`。
- ✅ production 缺 digest / tag-only / 格式错 / digest 与 repository 混写 → 全部 fail（fail-closed）。
- ✅ local/dev 仍允许 `global.imageTag`。
- ✅ 契约固化为 CI gate（verify-aiops-workflow-gates.sh sup2 断言 + test-image-digest-contracts.sh）。
- release 阶段（P1-REL1）以真实镜像 digest 注入 `global.imageDigests` 后部署。

## 5. 关联

- 下一步 P1-GOV1（branch protection）是**仓库设置**，需要 repo admin 权限（本机 `gh` 未登录），待用户提供凭据或手工开启。
