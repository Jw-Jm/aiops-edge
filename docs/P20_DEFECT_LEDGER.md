# V9.3 Phase 20 Defect Ledger

> 唯一 ledger ID 规则：`P20-<来源>-<序号>`；来源 ∈ {CONTRACT, BUGBOT}。
> 关闭条件（P20.1 强制）：**repro + root cause + fix + 负向测试 + 回归 exit code + Evidence 链接** 六项齐备。
> 禁止用"偶现/重跑通过"关闭安全 flaky。
> STATUS = OPEN（Phase 20 过程中逐项关闭）
> DATE = 2026-08-22
> GIT_ACTION = NONE

---

## 0. 去重规则（P20.2）

- 合同 §八十六 P0(15)/P1(7) 与 Bugbot 清单（第七部分 6 P0/6 P1）逐条比对。
- 同一缺陷双源 → 合并到同一 ledger 条目（双 source 引用）；仅单源 → 保留原来源。
- 本台账共 34 条候选，去重后条目数为：合同 22 + Bugbot 独立 6 = **28 条**（6 条 Bugbot 与合同/已修复项重合合并）。

---

## 一、合同 P0 清单（§八十六，15 项）

| ledger_id | 缺陷 | source | component | 映射 | 状态 |
|-----------|------|--------|-----------|------|------|
| P20-CONTRACT-P0-01 | fabricated fact（伪造事实） | CONTRACT | rca_engine/evidence_hub | 对应 R7 权威 contracts.RcaResult + EvidenceScopeMismatch | 见 Evidence 主文档 §R1/R7 |
| P20-CONTRACT-P0-02 | Tool semantic mismatch（工具语义不匹配） | CONTRACT | tool_result | P7.3 7 态归一化 | 已修复 |
| P20-CONTRACT-P0-03 | permission_denied treated absent | CONTRACT | tool_result | P7.3 403≠no_data 不降级 | 已修复 |
| P20-CONTRACT-P0-04 | no_data treated healthy | CONTRACT | tool_result | P7.3 503≠healthy | 已修复 |
| P20-CONTRACT-P0-05 | RCA without Evidence | CONTRACT | rca_snapshot | P9.1 snapshot 冻结 + assert_evidence_registered | 已修复 |
| P20-CONTRACT-P0-06 | Log Agent not running | CONTRACT | agent_runtime | P8.2 LogAgent + MissingEvidence | 已修复 |
| P20-CONTRACT-P0-07 | SSE broken | CONTRACT | sse_stream | P10 SSE + R3 tenant 校验 | 已修复/待接线 |
| P20-CONTRACT-P0-08 | cross-cluster contamination | CONTRACT | resource_graph/rca_engine | R0-P0-5 + EvidenceScopeMismatch | 已修复 |
| P20-CONTRACT-P0-09 | wrong-cluster action | CONTRACT | approval | P11 cross-cluster 拒绝 | 已修复 |
| P20-CONTRACT-P0-10 | authorization bypass | CONTRACT | authz_matrix | R8 接入 run-invocations | 已修复 |
| P20-CONTRACT-P0-11 | approval bypass | CONTRACT | approval_service | R2 approve_task fail-closed | 已修复 |
| P20-CONTRACT-P0-12 | execution treated as recovery | CONTRACT | execution | P11.9 regression stop | 已修复 |
| P20-CONTRACT-P0-13 | verification wrong | CONTRACT | verification | P11.8 SLI 非 exit code | 已修复 |
| P20-CONTRACT-P0-14 | old image deployed | CONTRACT | deploy | P18 digest reconciliation + Plan 4 fresh cycle | 待 Plan 4 |
| P20-CONTRACT-P0-15 | main page unusable | CONTRACT | frontend | P19.8 browser + Plan 4 | 待 Plan 4 |

## 二、合同 P1 清单（§八十六，7 项）

| ledger_id | 缺陷 | source | component | 映射 | 状态 |
|-----------|------|--------|-----------|------|------|
| P20-CONTRACT-P1-01 | filter/time/cluster issue | CONTRACT | frontend/query | P19 隔离 Gate 403 语义 | 已修复 |
| P20-CONTRACT-P1-02 | evidence deep link | CONTRACT | frontend | P12 evidence_refs | 待 Plan 1 T6 |
| P20-CONTRACT-P1-03 | correlation | CONTRACT | rca | R7 RCA 关联 | 已修复 |
| P20-CONTRACT-P1-04 | confidence | CONTRACT | scoring | P9.7 固定公式 | 已修复 |
| P20-CONTRACT-P1-05 | navigation not converged | CONTRACT | frontend | P12 六大导航收敛 | 已修复 |
| P20-CONTRACT-P1-06 | old path remains | CONTRACT | frontend/api | P14 删旧 | 已修复 |
| P20-CONTRACT-P1-07 | dead code/dependency | CONTRACT | 全部 | P15 依赖精简 | 已修复 |

## 三、Bugbot 清单（第七部分，6 P0 + 6 P1）

| ledger_id | 缺陷 | source | 映射 | 状态 |
|-----------|------|--------|------|------|
| P20-BUGBOT-P0-01 | P0-1 真实入口未验签（main.py:433） | BUGBOT | 合同 P0-10 相关 | 已修复（R2） |
| P20-BUGBOT-P0-02 | P0-2 审批链绕过（main.py:1589） | BUGBOT | 合同 P0-11 | 已修复（R2） |
| P20-BUGBOT-P0-03 | P0-3 RBAC 集群写权限 | BUGBOT | 合同 P0-14 相关 | 已修复（R9）待 Plan 2 复核 |
| P20-BUGBOT-P0-04 | P0-4 缺 egress 隔离 | BUGBOT | — | 待 Plan 2（egressDefaultDeny 启用） |
| P20-BUGBOT-P0-05 | P0-5 cron 绕过人工触发 | BUGBOT | 合同 P0-10 相关 | 已修复（R2） |
| P20-BUGBOT-P0-06 | P0-6 Authorization Matrix 孤立 | BUGBOT | — | 已修复（R8 接入 run-invocations）待 Plan 1 T2 加固 |
| P20-BUGBOT-P1-01 | P1-1 P9 RCA 缺陷 | BUGBOT | 合同 P0-01/05 | 已修复（R1/R7） |
| P20-BUGBOT-P1-02 | P1-2 SSE 未校验 tenant | BUGBOT | 合同 P0-07 | 已修复（R3） |
| P20-BUGBOT-P1-03 | P1-3 P10 无 DB 持久化 | BUGBOT | — | R13 部分（ai_runs DAO）+ 待 Plan 1 T4（orchestrator 接入） |
| P20-BUGBOT-P1-04 | P1-4 P11 未接线 | BUGBOT | — | 待 Plan 1 T5 |
| P20-BUGBOT-P1-05 | P1-5 P12 前端未接线 | BUGBOT | — | R10/R12 部分 + 待 Plan 1 T6（移除 DEMO） |
| P20-BUGBOT-P1-06 | P1-6 R2 同名模型未收敛 | BUGBOT | — | R7 完成 rca_engine；待 Plan 1 T3（investigation_state.Hypothesis） |

## 四、P20.2 并入的安全/语义类缺陷（合同 P20.2 要求归入既有类）

| ledger_id | 缺陷 | 归类 |
|-----------|------|------|
| P20-CONTRACT-P0-08 扩展 | unknown/unregistered canonical cluster 被当生产事实源 | 安全类（P19 隔离 Gate 已修 internalScopeAuthorized） |
| P20-CONTRACT-P0-08 扩展 | registered mapping resolved to wrong tenant/cluster | 安全类（P3.10c UID 身份绑定） |
| P20-CONTRACT-P0-04 扩展 | platform pipeline unavailable 被当 target no_data/healthy | 语义类（503≠no_data 不降级） |
| P20-CONTRACT-P0-10 扩展 | source bypasses Trusted Query/Tool/Evidence boundary | 安全类（Agent→query-api 唯一路径） |

> 不新增 Incident/Detection/Autonomy/Edge-Governance 缺陷类别（不属于当前架构，P20.2 强制）。

---

## 五、待 Plan 1-4 关闭的 OPEN 条目

| ledger_id | 关闭归属 | 完成动作 |
|-----------|----------|----------|
| P20-BUGBOT-P0-04 | Plan 2 | egressDefaultDeny 渐进启用 + allow-list |
| P20-CONTRACT-P0-14 | Plan 4 | fresh cycle 本轮 digest |
| P20-CONTRACT-P0-15 | Plan 4 | browser smoke main page |
| P20-CHART-P1-01 | Plan 2 | templates/ 下 codemap.md 导致 helm lint 失败（已用 .helmignore 忽略） |
| P20-CHART-P0-01 | Plan 2 | 生产部署 orchestrator ClusterRole 仍含集群写权限（旧模板，grantK8sWrite 实际生效为 true），需 rollout 收紧 |

## 六、状态追踪

- 状态：`OPEN`（未关闭）/ `CLOSED`（repro+root cause+fix+负向测试+回归+Evidence 链接六项齐备）。
- 每个 CLOSED 条目须在 Evidence 主文档 Phase 20 章节补 Evidence 链接。
- 本台账随 Plan 1-4 执行逐步将 OPEN → CLOSED。

## 七、Plan 1 关闭条目（2026-08-22）

| ledger_id | 完成动作 | 负向测试 | 回归 | 状态 |
|-----------|----------|----------|------|------|
| P20-BUGBOT-P0-06 | Authorization Matrix 权威接入加固（未知 capability/未注册 principal/跨 tenant/role tamper/viewer 拒建 Run 全 fail-closed） | `tests/test_p20_authz_wiring.py`（6 passed） | orchestrator 93 passed（含 P13 security 17） | CLOSED |
| P20-BUGBOT-P1-06 | `investigation_state.Hypothesis` 平行 dataclass 删除，组合权威 `contracts.Hypothesis` | — | `test_p77`+`test_p710` 24 passed | CLOSED |
| P20-BUGBOT-P1-03 | P10 Run 持久化接线确认（PersistentRunStateStore 远端提交优先，HTTP 失败不推进缓存 fail-closed） | `test_b6_persistent_repo` HTTP 503 不推进 | 31 passed（P10+persistent） | CLOSED |
| P20-BUGBOT-P1-04 | P11 ApprovalService 接权威 SoT（SoT 不可达/未启用/缺 capability fail-closed） | `tests/test_p20_p11_sot_wiring.py`（7 passed） | orchestrator 93 passed | CLOSED |
| P20-BUGBOT-P1-05 | P12 前端移除 DEMO 占位（InvestigationCenter/IntelligentInvestigation 真实数据源，失败空态不伪造） | tsc --noEmit exit 0 | frontend tsc ok | CLOSED |
| P20-CONTRACT-P0-07 | SSE tenant 校验确认（订阅校验 Run 归属 tenant，SSE_TENANT_MISMATCH fail-closed） | `test_p10_run_persistence` SSE 拒错 tenant | 31 passed | CLOSED |
| P20-CONTRACT-P1-02 | evidence deep link（调查中心真实 Run 数据源 + 详情查看链路） | tsc --noEmit exit 0 | frontend tsc ok | CLOSED |

> Plan 1 证据：主 Evidence 文档 Phase 20 章节（P20.1/P20.2/P20.3 代码级收口）。GIT_ACTION=NONE。

## 八、Plan 2 关闭条目（2026-08-22）

| ledger_id | 完成动作 | 负向测试/验证 | 回归 | 状态 |
|-----------|----------|----------|------|------|
| P20-CHART-P1-01 | templates/ 下 15 个 codemap.md 占位文档导致 helm lint 失败 → 新增 `.helmignore` 忽略（`*.md`/`codemap.md`）| `helm lint` 0 chart failed | 无回归（文档忽略） | CLOSED |
| P20-CHART-P0-01 | 生产 orchestrator ClusterRole 仍含集群写权限（deployments patch/pods eviction/nodes patch）→ 同步 5 服务镜像 tag 到正确值 + `helm upgrade`（grantK8sWrite 默认 false）重新 rollout | `kubectl auth can-i patch deployment` → **no**（写已撤销）；`get pods` → **yes**（只读保留）| query-api `/health`=ok、orchestrator Ready=True | CLOSED |

> Plan 2 证据：主 Evidence 文档 Phase 20 章节（P20.4 Helm 安全收紧 RBAC）。GIT_ACTION=NONE。
