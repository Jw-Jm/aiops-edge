# V9.3 Phase 20（缺陷收口 + 最终生产构建）设计 v0.2

> 执行合同：`aiops-agentic-v9.3-deepseek-execution-r4-manual-ai-trigger.md` §八十六（Phase 20）+ §八十七（Phase 21 相邻）。
> 本设计覆盖 P20.1-P20.5 + Gate 20。Phase 21（文档/Git）由后续独立设计承接。
> STATUS = DESIGN（v0.2，已闭环设计评审 2 P0 + 3 P1 准入问题）
> DATE = 2026-08-22
> GIT_ACTION = NONE（整个 Phase 20 不 commit/push）

---

## 0. 版本历史与评审闭环

| 版本 | 状态 | 变更 |
|------|------|------|
| v0.1 | REVISE_REQUIRED | 评审：Gate 6 缺 cutover 合同 / 双集群隐含授权 / egress 缺 allow-list / Ledger 缺去重规则 / Gate 缺确定性语义 |
| v0.2 | DESIGN | 闭环 2 P0 + 3 P1（见 §0.1），拆 4 份独立实施计划 |

### 0.1 评审准入问题闭环对照

| # | 级别 | 评审要求 | 闭环位置 |
|---|------|----------|----------|
| P0-1 | P0 | Gate 6 必须有独立 cutover 合同（writer/reader 分阶段、影子对账、precheck、成功/停止阈值、回滚、观察窗口、不可变 manifest hash；先写后读，每步单独 Go/No-Go；只停流量不删数据） | §6 Gate 6 Staged Cutover 合同 |
| P0-2 | P0 | 双集群不能被 P20 隐式授权；列为 Fresh Final Cycle 外部依赖，仅当 P19 B v0.2 通过并取得单独授权后纳入 Gate 20 输入 | §5.2 外部依赖 |
| P1-1 | P1 | default-deny egress 需精确 allow-list 与渐进 rollout（namespace/workload allow-list、canary、连通性 precheck、观察窗口、停止条件、Helm rollback revision） | §4 包1 |
| P1-2 | P1 | 缺陷台账可去重且可审计（唯一 ledger ID + 来源映射；关闭条件含 repro/根因/修复/负向测试/回归/Evidence 链接） | §3 Defect Ledger |
| P1-3 | P1 | 最终 Gate 确定性验收语义（精确仓库/命令/超时/环境版本；未授权一律 403、授权空才 no_data；语义测试取代关键词 grep） | §5 + §7 验收语义 |

---

## 1. 用户决策（已确认）

| 决策点 | 选择 | 说明 |
|--------|------|------|
| 缺陷收口范围 | **完整零缺陷** | 严格按合同 P20.3，P0=0/P1=0 才进 final cycle |
| Gate 重做深度 | **尽可能真实环境** | 真实 MySQL/query-api/orchestrator 进程重启/真实 K8s；不接新真实基础设施 |
| F5 红线边界 | **允许 Gate 6 cutover**（唯一生产变更） | 其余保持冻结；真实业务执行变更仍 NOT APPROVED；仅停流量不删数据 |
| 包1 部署 | **允许 rollout 收紧后 Helm** | 按 §4 渐进 rollout，非一次性收紧 |

### 1.1 明确冻结（评审重申）
- 真实 remediation / execution（业务执行变更）
- 非受控跨集群写入
- 数据删除与 reset（任何删除需单独授权，同 P17 模式）
- 自动进入 Phase 21

---

## 2. 总体流程（对齐合同 P20.1-P20.5 + Gate 20）

```text
P20.1 Defect Ledger       → 新建可去重可审计缺陷台账（§3）
P20.2 P0/P1 分类复查      → 逐项标注唯一 ledger ID/状态/归属，并入安全/语义类缺陷
P20.3 Zero-defect Gate    → 修复代码缺陷至 P0=0/P1=0（按 4 个整改包）
P20.4 Fresh Final Cycle   → full tests → new version → 重建5镜像 → deploy → fresh telemetry → real LLM → browser → smoke（禁止复用 P19；双集群为外部依赖）
P20.5 Final Identity Evidence → 记录 source_tree_hash/build_id/version/digests/deployed version/smoke run IDs
Gate 20                   → 所有 final evidence 来自本轮窗口 + 本轮 digest；Gate 后停止
```

---

## 3. P20.1 + P20.2：Defect Ledger（可去重、可审计）

新建 `docs/P20_DEFECT_LEDGER.md`，合并两条缺陷清单。

### 3.1 唯一 Ledger ID 与来源映射（P1-2 闭环）
- 每条合同缺陷（合同 §八十六 P0 15 项 + P1 7 项）建立**唯一 ledger ID**，格式 `P20-<来源>-<序号>`，如 `P20-CONTRACT-P0-01`。
- Bugbot 清单（第七部分 6 P0 + 6 P1）映射到 `P20-BUGBOT-P0-01` 等，与合同清单**逐条比对去重**：
  - 同一缺陷两种来源 → 合并到同一 ledger 条目，记录双来源引用。
  - 仅单一来源 → 保留原来源，标注无重合。
- 每条目记录：`ledger_id / severity / source(合同|Bugbot|双源) / component / repro / root cause / fix / tests(负向) / regression / evidence_link / status`。

### 3.2 关闭条件（证明 P0=0/P1=0）
每条缺陷关闭必须同时满足：
1. **可复现 repro**（记录步骤或输入）。
2. **根因**（定位到代码/配置）。
3. **修复**（指向具体 diff/文件变更）。
4. **负向测试**（证明缺陷不再触发；安全类必须有负向断言）。
5. **回归结果**（本轮全量测试 exit code，无回归）。
6. **Evidence 链接**（指向本轮主 Evidence 文档 Phase 20 章节的具体行）。

**不得用"偶现/重跑通过"关闭安全 flaky**（合同 P20.1 强制）。

### 3.3 分类复查（P20.2）
- 并入既有安全/语义类缺陷：`unknown/unregistered canonical cluster`、`registered mapping resolved to wrong tenant/cluster`、`platform pipeline unavailable treated as no_data/healthy`、`source bypasses Trusted Query/Tool/Evidence boundary`。
- **不新增** Incident/Detection/Autonomy/Edge-Governance 缺陷类别。

---

## 4. P20.3：零缺陷修复（4 个整改包）

### 包1：Helm 安全收紧（渐进 rollout，已授权，P1-1 闭环）

**RBAC 撤销写权限**：orchestrator ClusterRole 撤销 workload patch/scale、pod eviction、node patch（仅保留只读）。

**移除 orchestrator DB 凭据**：orchestrator 不直连 DB，走 query-api boundary；移除 DB 凭据注入。

**default-deny egress（渐进 rollout，非一次性收紧）**：
- **固定 allow-list**（namespace/workload 维度），最小放行：
  - DNS：`kube-dns/coredns`（TCP/UDP 53）
  - Kubernetes API Server：`kube-apiserver`
  - query-api 服务
  - LLM 出口（deepseek API 域名）
  - 中心遥测后端（VM/VLogs/CH，若跨 ns 需要）
  - 镜像拉取（registry，若在 cluster 内）
- **canary**：先对非关键 workload 启用 egress default-deny，观察稳定后再推广。
- **连通性 precheck**：启用前用探针验证 allow-list 完整，DNS/API/LLM/遥测均可达。
- **观察窗口**：每批 rollout 后观察 N 分钟（指标/日志无异常）。
- **停止条件**：出现核心链路（DNS/API/query-api/LLM/遥测）连通失败 → 立即停止并回滚。
- **Helm rollback revision**：记录部署前 revision，异常时 `helm rollback <release> <prev_revision>`。

### 包2：Authorization Matrix 权威接入
- Authorization Matrix 接入 query-api 权威身份 + 所有资源入口（消除孤立测试夹具）。
- ManualBoundary 唯一 Run 创建入口。
- Approval 绑定 tenant/cluster/action hash/version/risk/resourceVersion。

### 包3：模型收敛 + 持久化接线
- `investigation_state.Hypothesis` 收敛到权威 `contracts.Hypothesis`（消除同名平行模型）。
- P10 Run Store/SSE 接 query-api 持久化 + Run tenant 校验。
- P11 Approval/Execution 接 query-api 权威 SoT。
- P12 Run API/SSE 页面真实接线（移除 DEMO_RUNS/DEMO_DETAIL/setTimeout）。

### 包4：重做 Gate 9-13（尽可能真实环境，语义验收见 §7）
对 **Gate 9 / 10 / 11 / 12 / 13** 逐个：重新跑全量测试（本轮窗口+exit code）+ 对照合同 Entry Criteria 逐条复验 + 真实栈验证（真实 MySQL/进程重启/真实 K8s 只读）+ 产出本轮 Gate 评审证据。
**Gate 6 cutover 为独立合同（§6），不作为本包的一部分，需单独授权。**

---

## 5. P20.4 Fresh Final Cycle（禁止复用 P19）

### 5.1 本轮完整链路（逐项记录 exit code/digest）

```text
full tests（5 仓库，精确命令见 §7）
→ new version（如 v1.2.0-p20-<sha8>）
→ source_tree_hash 重算
→ 重建 5 镜像（query-api/ingest/event-collector/orchestrator/frontend）
→ deploy 到 orbstack（含包1 收紧后 Helm）
→ fresh telemetry（受控注入 canonical 数据，验证 VM/VLogs/CH 三写闭环）
→ real LLM smoke（deepseek 真实推理）
→ browser smoke（前端主旅程）
→ platform-self + registered-external source smoke
```

### 5.2 外部依赖（P0-2 闭环：双集群不可被 P20 隐含授权）
- **two-cluster isolation 不是 P20 默认输入的组成部分**。
- 仅当 **P19 B v0.2** 设计通过评审且取得**独立环境授权**后，才纳入 Gate 20 输入。
- 在本 P20 设计下，two-cluster 相关验证**挂起**，不计入本轮 Gate 20 证据，除非独立授权到位。
- P20.4 本轮默认执行的是 **orbstack 单集群** + platform-self/registered-external smoke。

---

## 6. Gate 6 Staged Cutover 合同（P0-1 闭环：独立 spec）

> 这是 F5 的唯一例外。作为独立部署授权对象，不隐含在 P20.3 或 P20.4 中。

### 6.1 原则
- **先写后读**：先切 writer（new 写），影子对账通过后再切 reader。
- **只停流量，不删数据**：停 legacy writer/reader 流量；legacy 数据与证据保留（不 TRUNCATE/不删除）。
- **每步单独 Go/No-Go**：每个 writer/reader 阶段需用户单独执行授权，不能一次批量放行。

### 6.2 分阶段步骤

| 阶段 | 动作 | 成功阈值 | 停止条件 | 授权 |
|------|------|----------|----------|------|
| **S0 PRECHECK** | 校验 canonical 身份、new backend 可写、schema-migrator 就绪、迁移版本 | 全部通过 | 任一 fail → ABORT | 用户授权 |
| **S1 Writer 影子写** | new writer 启用，legacy writer 保留，双写 | VM/VLogs 有 canonical 数据；legacy 仍写 | 影子数据缺失 → 停 new writer | 用户授权 |
| **S2 Writer 影子对账** | 对比 VM/VLogs/CH 同源数据，校验标签契约 | 数据一致/可解释差异 | 标签/值不一致 → 回滚 | 用户授权 |
| **S3 Writer 流量切换** | 停 legacy writer 流量（legacy 仅保留数据） | new writer 全量承接，无写失败 | 写失败率 > 阈值 → 回滚 writer | 用户授权 |
| **S4 Reader 影子读** | new reader 启用，legacy reader 保留 | new reader 返回正确数据 | 读取异常 → 停 new reader | 用户授权 |
| **S5 Reader 流量切换** | 停 legacy reader 流量 | new reader 全量承接，语义正确 | 语义错误 → 回滚 reader | 用户授权 |
| **S6 观察窗口** | 观察 N 分钟（指标/日志/查询） | 无回归、无错误 | 异常 → 回滚到 prev revision | 用户授权 |

### 6.3 不可变 manifest hash
- 每个阶段执行前生成**不可变 manifest**（含 cluster/tenant/镜像 tag/阶段动作/期望状态），计算 SHA-256。
- **manifest hash 是唯一部署授权对象**：授权时引用 hash；任何字段变化 → hash 变 → 须重新授权。
- 每个 writer/reader 阶段均有自己的 manifest + hash + 授权记录。

### 6.4 回滚
- 每阶段记录 Helm revision 与 writer/reader 模式。
- 回滚：`helm rollback` 到 prev revision + 恢复 writer/reader 模式；new backend 停用，legacy 恢复承接。
- 不删除已写入的 new 数据（保留供审计），legacy 数据全程保留。

---

## 7. 验收语义与红线证明（P1-3 闭环：确定性）

### 7.1 精确测试命令（5 仓库全量）
| 仓库 | 命令 | 超时 | 环境 |
|------|------|------|------|
| ai-apm-query-go | `go test ./...` + `go vet ./...` | 15min | go <版本>（按 go.mod） |
| ai-apm-ingest-go | `go test ./...` + `go vet ./...` | 15min | 同上 |
| ai-event-collector | `go test ./...` + `go vet ./...` | 15min | 同上 |
| ai-orchestrator | `python -m pytest <本轮范围>` + `ruff check` | 30min | Python <版本>（requirements） |
| observability-frontend | `npx tsc --noEmit` + `npx vite build` | 15min | node <版本> |

- 每仓库记录**精确 exit code**，写入本轮 Evidence。
- 环境版本（go/python/node）记录到本轮 Identity Evidence。

### 7.2 403 / no_data 语义（确定性）
- **未授权**（错误 tenant / cluster / capability）→ **一律 403**。
- **已授权且结果为空** → `no_data`（200 + NO_DATA）。
- 两者严格区分，不降级、不混写（对齐 P19 双集群隔离 Gate 已修复的 `internalScopeAuthorized`）。

### 7.3 红线证明（语义测试取代关键词 grep）
- **禁止仅用关键词 grep 证明红线**。
- 红线证明改为：**禁止调用路径 + 运行时策略 + 负向测试**：
  - Agent/Planner 代码**静态断言无执行类依赖**（调用路径分析，非字符串 grep）。
  - 运行时策略：Authorization Matrix / 权限层 fail-closed 断言。
  - 负向测试：越权尝试一律被拒（403/denied），而非"无匹配字符串"。
- `grep` 仅作辅助，不作为通过标准。

---

## 8. 交付物

| 交付物 | 路径 |
|--------|------|
| Defect Ledger | `docs/P20_DEFECT_LEDGER.md` |
| 设计文档 | 本文档（v0.2） |
| Gate 6 Cutover 合同 | 本文档 §6（作为独立授权对象） |
| 实现计划（writing-plans 产出） | `docs/superpowers/plans/`（4 份） |
| Gate 9-13 重做 + Gate 20 证据 | 追加主 Evidence 文档 Phase 20 章节 |

---

## 9. 4 份独立实施计划（评审建议）

| # | 计划 | 范围 | 验收 |
|---|------|------|------|
| 1 | **Defect Ledger + Authorization + 模型/持久化整改** | P20.1/P20.2 + 包2 + 包3 代码整改 | 代码缺陷修复、负向测试、回归、Ledger 关闭条目 |
| 2 | **Helm 安全收紧 acceptance rollout** | 包1（RBAC/DB 凭据/渐进 egress） | helm lint + 渐进 rollout + 连通性 precheck + 可回滚 |
| 3 | **独立 Gate 6 staged cutover** | §6 合同（writer/reader 分阶段） | 每阶段 Go/No-Go + manifest hash + 只停流量不删数据 |
| 4 | **Gate 9-13 重做 + Fresh Final Cycle** | 包4 + P20.4/P20.5/Gate 20 | 语义验收 + 本轮窗口证据；双集群为显式外部依赖 |
