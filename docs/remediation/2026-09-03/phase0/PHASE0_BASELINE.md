# Phase 0 整改基线冻结记录（2026-09-03）

> 依据：《AIOps_全面技术审核与生产整改最终报告_2026-09-03 (1).md》§24 Phase 0。
> 本文件为新建整改证据，未修改任何历史报告。

## P0.1 开始 commit（冻结）

| 项 | 值 |
|---|---|
| 整改基线 commit | `bd93acd8974b3b5132061698ab517867640ba96c`（"bound slow-client writes with WriteTimeout, keep SSE alive"） |
| 与报告基线一致性 | **一致**。`origin/main` = `bd93acd`，报告审核基线 = `bd93acd` |
| 本地 main HEAD | `6cbe55f`（仅比基线多 1 个未推送 docs commit："archive full audit report with fix progress log"，+26 行文档，无代码差异）。该 commit 不带入整改分支，避免基线漂移 |

## P0.2 工作区确认

- 建分支前工作区干净：仅 1 个 untracked 文件 `AIOps_全面技术审核与生产整改最终报告_2026-09-03 (1).md`（审核报告本身）。
- 无未提交代码修改，无 stash 冲突。

## P0.3 整改分支

```text
branch: remediation/p1-release-blockers-20260903
from:   bd93acd8974b3b5132061698ab517867640ba96c
```

已创建并切换。分支未推送远端（推送时机待整改产出后统一决定）。

## P0.4 当前失败 CI evidence

GitHub Actions run（api.github.com，公开仓库无需认证）：

| 项 | 值 |
|---|---|
| run id | `33738424444` |
| workflow | `aiops-workflow-gates`（`.github/workflows/aiops-workflow-gates.yml`） |
| head sha | `bd93acd8974b3b5132061698ab517867640ba96c` |
| status / conclusion | `completed` / **`failure`** |
| created_at | 2026-09-03T09:21:56Z |
| 失败 job / step | job `verify` → step **`Verify workflow gates`** = failure |

已固化到本目录：
- `ci_runs_list.json`（近 10 次 run 列表）
- `ci_run_33738424444.json`（run 元数据）
- `ci_run_33738424444_jobs.json`（job/step 级结论）

限制说明：完整日志 zip 下载端点需 repo admin 凭据（本机 `gh` 未登录），改用 job/step API + 本地复现固化证据。

### 本地复现（P1-CI1 失败点）

| 项 | 值 |
|---|---|
| 本机环境 | go1.26.4 darwin/arm64 |
| 命令 | `go test ./internal/api/ -run TestEWMAConstant -count=1 -v` |
| 本机结果 | **PASS**（见 `local_repro_p1ci1_testewma.txt`） |
| CI 结果 | FAIL：`EWMA constant = 2.9999999999999996, want 3` |

平台差异解读：断言 `if v != 3`（capacity_test.go:46）为精确浮点比较。ARM64 与 CI x86_64 浮点求值顺序/FMA 差异导致同一代码一边恰好等于 3、一边差 1 ULP。**本机 PASS 不能证明 CI 健康**，精确比较本身即为缺陷，P1-CI1 修复（`math.Abs(v-3) > 1e-12` + 非整数输入回归测试）结论不变。

## P0.5 历史报告完整性

- 未修改任何历史报告/审核文档。
- 报告 §26 所列"假完成"行为在 Phase 0 未发生。

## Phase 0 完成清单

| 步骤 | 状态 |
|---|---|
| 1. 记录开始 commit | ✅ `bd93acd`，与报告基线一致 |
| 2. 确认工作区 | ✅ 干净（仅 untracked 审核报告） |
| 3. 建整改分支 | ✅ `remediation/p1-release-blockers-20260903` |
| 4. 保存失败 CI evidence | ✅ run 33738424444 (failure) 3 份 JSON + 本地复现记录 |
| 5. 不修改历史报告 | ✅ 本文件为新建，历史报告未动 |

## 下一步（待授权或按序继续）

Phase 1 顺序：P1-S1 → P1-CI1 → P1-SCA1 → P1-SUP1 → P1-SUP2 → P1-GOV1 → 全量 CI success。
