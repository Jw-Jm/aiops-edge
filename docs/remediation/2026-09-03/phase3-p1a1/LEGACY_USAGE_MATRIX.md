# P1-A1 Legacy runtime 使用矩阵（2026-09-03）

> 依据审核报告 §12。原则：**删除优于关闭**；先真实调用图，再删除。

## 1. Legacy 文件矩阵

| 文件 | 生产 import | 测试 import | Helm/入口 | 裁决 |
|---|---|---|---|---|
| `rca.py` | **是**：main / orchestrator.py / apps/investigation.py（node_rca/full_rca_analysis 生产 RCA 端点） | 是（大量） | — | **保留**（生产主链；B1 范围裁决保留） |
| `rca_engine.py` | 是：main / apps/investigation | 是 | — | **保留**（P9 新 RCA 引擎，RcaEngine 生产链） |
| `rca_engine_legacy.py` | **否（直接）**，但 `rca_engine/__init__.py::_load_legacy` **动态加载桥目标**：local 迁移测试经它提供 `RcaEngine` 兼容符号（test_p16_12_rca_scenarios `from rca_engine import RcaEngine`） | test_p16 / test_rca_engine_isolation（隔离断言 + local compat） | .dockerignore 已排除（镜像 V2-only） | **保留**（明确为 V2 桥的兼容加载目标；物理删除需先移除 rca_engine 桥的 RcaEngine 符号来源——独立 legacy-removal 专项） |
| `rca_production.py` | **无**（被测试 import） | test_p9_production_adapter / test_r2_gate | — | 保留（生产适配层，测试覆盖）；main 接线属真实环境 Integration Gate |
| `rca_snapshot.py` | 经 rca_engine | 是 | — | 保留 |
| `investigator.py` | 是：main（legacy flow investigator，开关 INVESTIGATOR_ENABLED） | test_investigator | — | 保留→收敛（见 §3） |
| `investigation_app.py` | 是：worker 入口（`python -m investigation_app`） | 是 | investigation-worker CMD | 保留 |
| `investigation_runtime.py` | 是：main / apps/investigation | 是 | — | 保留（新 worker runtime） |
| `investigation_dispatcher.py` | 是：main / apps/investigation | 是 | — | 保留 |
| `investigation_state.py` | 经 runtime | 是 | — | 保留 |
| `multicluster_demo.py` | **无** | **无** | .dockerignore 已排除 | **已物理删除**（本轮） |

## 2. Legacy 开关矩阵

| 开关 | 代码引用 | Helm 注入 | 生产默认 | 裁决 |
|---|---|---|---|---|
| `LEGACY_FLOW_RUNTIME_ENABLED` | flow_api/investigator/main | ai-orchestrator + investigation-worker | 0 | 收敛后随 legacy 删除 Helm env |
| `LEGACY_DIRECT_MUTATIONS_ENABLED` | main/investigator | 同上 | 0 | 同上（报告 §3.1 禁重新开启） |
| `INVESTIGATOR_ENABLED` | main（旧 investigator 加载门） | ai-orchestrator | 0 | 新 worker 用 INVESTIGATION_RUNTIME；investigator.py 收敛后删 |
| `INVESTIGATION_RUNTIME_ENABLED` | apps/investigation | 两者 | 1 | 保留（新 worker runtime） |

## 3. 本轮执行（安全子集）

- **物理删除孤儿**：`multicluster_demo.py`（确认无任何源码/测试引用，.dockerignore 早已排除镜像）。✅
- **矩阵修正**：`rca_engine_legacy.py` 初判孤儿，实测是 `rca_engine` V2 包的**兼容桥动态加载目标**（local 迁移经它导出 `RcaEngine` 符号）→ 已恢复，裁决"保留至 legacy-removal 专项"。删除它会破坏 test_p16/隔离测试的 compat 语义。
- **文档化矩阵**：investigator.py 旧 flow 与 apps/investigation 新 worker 的收敛（旧 investigator 由 INVESTIGATOR_ENABLED 门控、main 加载 legacy flow）需要 main 入口重构（legacy flow endpoints 删除后移除加载逻辑），属后续 legacy-removal 任务（涉及 API 端点删改，需独立批准——按 §12.2.1 全项标记后再动 main）。
- Helm 已按 report 持续断言 legacy mutation off（policy gate）。

## 4. 后续 legacy 删除候选（待矩阵补全 + 授权）

- main/flow_api legacy flow endpoints（run-legacy 已于 P14 删除，investigator legacy 主链仍在 main）
- `INVESTIGATOR_ENABLED`/`LEGACY_FLOW_RUNTIME_ENABLED` 的 Helm env 注入清理（等代码删后）
