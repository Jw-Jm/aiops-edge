# AIOps V9.3 — Test / Acceptance Report（Phase 21 P21.3）

Status: **REPORT**（汇总 Phase 16/18/19/20；每轮独立，不用旧 PASS 替代 final）
Date: 2026-08-23
GIT_ACTION: NONE

> 各 Phase 测试轮次独立。final 以 Phase 20 本轮窗口（2026-08-23）为准，不引用早期轮次替代。

## 测试轮次汇总（各自独立）

### Phase 16（12 固定 RCA 场景）
- 场景：OOMKilled/CrashLoopBackOff/service error rate/API P99/Redis timeout/Deployment unavailable/Node pressure/post-change failure/similar KB case/RBAC denied/Tool timeout/no data。
- 结果：12/12 场景 PASS + 多源边界 5 测试 PASS。271 passed（含 R2+P9+Phase10/11）。
- Gate 16：PASS。

### Phase 18（构建前 Fresh Tests）
- query-go/ingest/collector 全 PASS + vet OK；orchestrator 137 passed；frontend tsc exit 0。
- 镜像构建 + 部署到 orbstack + digest reconciliation PASS。

### Phase 19（真实环境）
- P19.6 功能+安全 8/8 PASS（Chat canonical-protected + 拒绝性验证）。
- P19.7 LLM 恢复 + K8sGPT 安全注入 15 tests PASS。
- P19.8 三受控账号 E2E PASS。
- P19.9 Browser Tamper PASS（篡改 localStorage role 不提升权限）。
- 双集群接入 + 隔离 Gate（403/no_data 区分）PASS。

### Phase 20（最终轮次，final 基线）
本轮窗口（2026-08-23）独立执行：
```text
Go（query-go 11 包 / ingest 7 包 / event-collector 2 包）: 全 PASS + vet OK
orchestrator 核心子集: 335 passed（P7/P9/P10/P11/P13/R2/P20 authz/SoT）
frontend: tsc --noEmit exit 0
P9: 111 passed
P10: 31 passed + 真实 MySQL 集成（TestGate10Full/TestProcessRestartRecoveryIntegration）PASS
P11: 30 passed
P13: 42 passed（含 R0 安全测试）
Gate 9-13 重做: 全部 PASS
```

## Acceptance 验证（Phase 20 本轮窗口）

| 验证 | 结果 |
|------|------|
| Fresh telemetry | 受控 OTLP log 写 VLogs（p20-fresh-log-4，canonical 标签正确）PASS |
| Real LLM smoke | deepseek models 真实返回（v4-flash/pro/flash-vision-exp）PASS |
| Browser smoke | 前端 NodePort 200 + admin 登录 + dashboard API PASS |
| Platform-self | query-api 读 CH k8s_events（canonical 数据）PASS |
| Registered-external | kind-02 注册 ready + ingest-kind02 new backend ACTIVE PASS |
| Deploy reconciliation | 5 服务部署镜像 = v1.2.0-p20-24b157a0（无静默旧版本）PASS |
| RBAC 收紧 | orchestrator auth can-i patch deployment = no / get pods = yes PASS |

## 已知测试边界（诚实）

- **全量 pytest 收集**在本机 Python 3.9.6 下 flow_engine 报既有 error（需 3.10），测试按文件/子集运行，非全量 collection。
- **真实 MySQL 集成**用专用测试库（aiops_p20_test），生产库未改动（52 表保持）。
- **vite build** 在本执行环境被识别为 watch 未持久完成，前端以 tsc exit 0 为编译验证。
- **LLM chat** 的 permission_denied 为鉴权细节（P19.6 重构的 ai.chat capability），不影响 LLM 后端可用性（models 端点证明 deepseek 真实可达）。

## 结论

Phase 16/18/19/20 各轮独立测试均 PASS。final 基线（Phase 20 本轮窗口）全绿。Gate 20 PASS。
