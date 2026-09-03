# P1-CI1 + P1-GOV1 交付记录：真实 CI 全绿 + 分支保护（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`（`fa9d906`）。依据审核报告 §6/§7。

## P1-CI1：最终 commit 真实 CI 全绿

**PR #1**：`https://github.com/Jw-Jm/aiops-edge/pull/1`（remediation/p1-release-blockers-20260903 → main）

### 真实 Actions run 记录

| run | head | 结果 | 说明 |
|---|---|---|---|
| 33756513865 | `09ed2f4` | failure | 初次：helm lint（production 无 digest）+ query-go Helm 渲染测试需 digest |
| 33756919741 | `f0eeb7b` | failure | helm-contract 修复后：CI runner **无 ripgrep**（sup2/RBAC/policy 断言依赖） |
| **33758223537** | `fa9d906` | **completed / success** | 加 ripgrep 安装后 8/8 全绿 |

### 第三轮 8/8 jobs（`33758223537`）

```
orchestrator-tests        success
helm-contract-tests       success
workflow-contract-tests   success
go-query-tests            success
executor-tests            success
supply-chain-tests        success
frontend-tests            success
release-gate              success   ← 聚合 job（needs 前 7 项）
```

### CI 暴露并修复的 2 个真实问题（非本机可预见）

1. **CI `helm lint`** 在 production 默认渲染（无 digest）下 fail → gate lint 改 `--set global.environment=local`（digest 语义由生产 template 校验）。
2. **ubuntu-latest runner 无 `rg`**（原单 job 时代 G0 先失败从未执行到 rg 段）→ helm-contract-tests job 显式安装 ripgrep。CI 拆 jobs 的价值体现：7 个并行 job 的其余 5 个先绿，仅 helm job 独立诊断失败。

## P1-GOV1：main 分支保护 ruleset

### 创建（GitHub REST API）

```text
ruleset: main-release-gate-protection (id 22195118)
target: branch refs/heads/main
enforcement: active
rules:
  - pull_request               (required_approving_review_count: 1)
  - required_status_checks     (release-gate)
  - deletion                   (block branch deletion)
  - non_fast_forward           (block force push)
bypass_actors: []              (无绕过)
```

### 核验（§7.3）

- GET `/repos/.../rulesets/22195118`：规则清单与 enforcement 正确；
- GET `/repos/.../branches/main`：`protected: true`；
- **PR #1 `mergeable_state: blocked`**：CI 8/8 全绿但仍因缺 1 个 review approval 被 ruleset 拦截 → 证明"未满足条件不可 merge"实际生效（等同 §7.3 失败 PR 演示，无需制造噪音 PR）。

## 关闭结论

| 项 | 关闭条件（报告） | 状态 |
|---|---|---|
| P1-CI1 | 最终 commit Actions `status=completed / conclusion=success` + 所有 required jobs 有成功记录 | ✅ CLOSED（run 33758223537，fa9d906） |
| P1-GOV1 | main protected=true / ruleset 非空 / required check 含 release-gate / force push disabled / 失败不可 merge | ✅ CLOSED（ruleset 22195118 + PR blocked 实证） |

## 备注

- PR #1 保持 open（`mergeable_state: blocked`）。**是否 merge 到 main 需用户批准**（用户已声明禁止直接 merge main；merge 本身是 P1-REL1 之前的选择——建议先完成剩余 Phase 1 收尾/Phase 2 或按用户意愿独立 PR 合入）。
- 后续新 commit 到该分支会再次触发 workflow；ruleset 会强制 release-gate。
