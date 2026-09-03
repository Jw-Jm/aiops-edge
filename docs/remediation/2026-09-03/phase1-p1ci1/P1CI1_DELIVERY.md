# P1-CI1 交付记录：修复 CI 失败 + 结构整改（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`（基于 `bd93acd`）。依据审核报告 §6。

## 1. 修改文件清单

| 文件 | 变更 |
|---|---|
| `ai-apm-query-go/internal/api/capacity_test.go` | `TestEWMAConstant` 精确比较→误差断言；新增 `TestEWMAConstantNonInteger` |
| `deploy/scripts/verify-aiops-workflow-gates.sh` | 支持 `AIOPS_GATE_STAGES` 分段执行（默认全部段，本地行为不变）；helm 存在性检查移入 helm 段 |
| `.github/workflows/aiops-workflow-gates.yml` | 单 job 拆为 6 独立 jobs + `release-gate` 聚合；Python 3.14→3.12 |

## 2. 根因

1. **测试失败根因**：`TestEWMAConstant` 用 `v != 3` 精确浮点比较。CI x86_64 上 `0.3*3+(1-0.3)*3` 累积出 `2.9999999999999996`；本机 ARM64 恰好等于 3（本地复现 PASS，见 Phase 0 evidence）——平台相关浮点实现差异，断言本身不成立。
2. **结构根因**：所有门禁挤在一个 job 的单脚本内，G0 失败导致 G0.5-G5 全部无诊断产出。
3. **附带发现**：本机 `.venv-312` 缺 `requirements-dev.txt` 中的 `pytest-asyncio`，导致 19 个 async/mtls 测试假失败（补装后 1322 passed 全绿）——**19 个"既有失败"实为本地环境不完整，非代码缺陷**，Phase 0/1 阶段的失败基线结论据此修正。

## 3. 代码修复

- 误差断言 `math.Abs(v-3) > 1e-12`；不修改 EWMA 生产算法、不引入 rounding、不删测试。
- 新增 `TestEWMAConstantNonInteger`（series=[0.1,...] 与 0.1 误差 <1e-12），防止后续以 rounding 让断言变绿而破坏生产精度。
- workflow：`go-query-tests / workflow-contract-tests / orchestrator-tests / executor-tests / frontend-tests / helm-contract-tests` 独立跑，`release-gate` needs 全部（报告 §6.3 结构）。
- Python 版本统一 3.12（报告 P1-SUP1 §8.2/8.7 要求 CI=production minor 版本；顺带落地于 workflow，避免二次改动）。
- CI 内 venv 命名 `.venv-ci`，经 `AIOPS_PYTHON` 显式传入 gate 脚本（消除 `.venv314` 命名耦合）。

## 4. 新增/修改测试

- 修改 `TestEWMAConstant`（更强断言：所有平台下误差必须 <1e-12，而非"恰好等于"）。
- 新增 `TestEWMAConstantNonInteger` 非整数回归。

## 5. 执行过的验证命令与结果

```bash
# EWMA 修复
go test ./internal/api/ -run 'TestEWMA' -count=1 -v   # 全 PASS（含新增非整数测试）
go vet ./internal/api/                                 # ok

# gate 脚本分段化
bash -n verify-aiops-workflow-gates.sh                 # 语法 OK
AIOPS_GATE_STAGES=go,workflow-contracts ... gates.sh   # G0 + G0.5 passed
AIOPS_GATE_STAGES=executor ... gates.sh                # G5 passed

# CI orchestrator-tests job 完整本地复现（fresh venv + Python 3.12 + lock + dev）
python3.12 -m venv /tmp/aiops-venv-test
pip install -r requirements-lock.txt -r requirements-dev.txt   # exit=0
AIOPS_GATE_STAGES=orchestrator AIOPS_PYTHON=... gates.sh
# → 1322 passed, 1 skipped, 0 failed
```

## 6. 架构契约 / 兼容性

- 无架构契约变更。gate 脚本默认段=全部，本地/既有调用行为不变。
- workflow Python 3.14→3.12 是 P1-SUP1 明确要求的收敛；fresh 3.12 venv + lock 全绿已验证。

## 7. 关闭结论：PARTIAL

| 关闭条件（报告 §6.4） | 状态 |
|---|---|
| 测试断言修复 + flaky 根因消除 | ✅ CLOSED |
| CI 拆独立 jobs + release-gate | ✅ CLOSED |
| 最终 commit Actions `status=completed / conclusion=success` | ⏳ 待 push 后由真实 CI run 证明（push 时机待用户授权；届时还要 P1-SCA1/P1-SUP1/P1-SUP2 项完成后才能全绿——特别是 frontend npm audit 当前有 critical/high 未 triage，且 P2-F1 workflow E2E 重写在 Phase 2） |

**注**：本次本地已可复现全部 CI job 的成功（go/orchestrator/executor/workflow-contracts）；frontend 与 helm 段在本地未跑（frontend npm test/build、helm 模板渲染在 CI 环境执行；helm 段逻辑未改动，基线 CI 已含该段逻辑）。
