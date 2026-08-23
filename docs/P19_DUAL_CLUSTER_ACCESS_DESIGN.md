# Phase 19 真实双集群接入设计（B 项）— v0.2

- **STATUS**: DESIGN v0.2（已关闭 2 P0 + 4 P1，待授权只读接入与隔离复验）
- **日期**: 2026-08-22
- **目标**: 接入 `aiops-kind-02` 作为第二真实集群，完成 P19.3（Two-cluster Isolation）与跨集群 RCA 的真实环境复验（**只读接入**，禁止跨集群写/执行）。

---

## 0. 评审裁决（v0.1 → v0.2）
- 设计方向正确，暂不批准部署。v0.2 关闭 2 P0 + 4 P1 后，**可授权在两个 acceptance 集群执行只读接入与隔离复验**。
- 新增固定项（§8）：otel-collector 必选/收缩、CH 表 owner、非 canonical 测试 tenant、telemetry 限速与容量停止条件、K8sGPT 跨集群凭据移出必经 Gate。

---

## 1. 当前现状（实测）
- `aiops-kind-02` 集群存在且 Ready（control-plane v1.35.0），**无 AIOps 采集栈**（无 observability ns、无 ingest/event-collector/otel）。
- 主集群 orbstack：完整 AIOps 栈，**单一中心数据面** = `victoria-metrics`/`victoria-logs`/`clickhouse`（observability 命名空间 in-cluster 服务）。
- 中心 CH 表：`k8s_events`/`alert_events`/`log_records`/`trace_spans`/`service_topology`（owner = 中心数据面，已批准存储，**非 P14 删除的 legacy CH writer**）。
- canonical tenant `7ed01afc-...` 存在。

## 2. [P0.1] 本地采集身份 vs 中央访问凭据（拆分）
**kind-02 内的 Secret 不能被主集群 query-api/orchestrator 读取；kind-02 collector 访问本集群 API 不用 kubeconfig。**

- **collector 本地身份**：kind-02 内的 `event-collector`/`ingest`/`otel-collector` 用 **kind-02 in-cluster ServiceAccount** 访问 kind-02 API（读事件/资源），**不使用 kubeconfig**，不写任何 credential Secret 于 kind-02。
- **中央访问凭据（仅按需）**：只有当**中央 connector / K8sGPT** 确实有**跨集群 API 访问**需求时，才使用位于**中央受控 secret provider**（orbstack `observability`）的最小权限 `credential_ref`。明确：
  - **读取者**：query-api 的 boundary client（`k8sboundary` SA），经 Cluster Registry `credential_ref` 引用。
  - **RBAC**：`k8sboundary-read`（nodes/pods/events 等只读），在 kind-02 侧以 ClusterRoleBinding 授予。
  - **轮换**：central `kubeconfig-kind-02` Secret 用**永不过期** SA token（同 C 项机制）。
  - **撤销**：回滚/退役时 revoke SA token + 删除 ClusterRoleBinding。
- **不在 kind-02 存储中央凭据**；kind-02 内无跨集群访问凭据。

## 3. [P0.2] 固定单一中心数据面与写入信任链
**VM/VLogs/CH 是共享中心后端，不在 kind-02 再建第二套 SoT。**

- **数据面唯一**：所有集群采集（orbstack + kind-02）都写入 **orbstack 的 VM/VLogs/CH**（中心数据面）。kind-02 **不部署** VM/VLogs/CH。
- **collector → 中心端点信任链**：
  - **网络出口**：kind-02 collector 通过中心网关/NodePort/service 出口到 orbstack 的 `victoria-metrics`/`victoria-logs`/`clickhouse`（跨集群网络；需打通 orbstack 侧 LoadBalancer/NodePort + 安全出口）。
  - **TLS/mTLS 或服务身份**：collector 到中心端点的连接用 **mTLS**（mutual cert）或**受控服务身份**（如 SA 签名 token），**不允许明文 HTTP 裸写**。
  - **服务端 cluster_id/tenant_id 校验**：中心端点（ingest/query-api）在**服务端**校验写入者身份，并**用服务端权威的 cluster_id/tenant_id 打标**——**collector 提供的标签不能成为可伪造的身份来源**。采集器只能带"负载内容"，不能自证 cluster 归属；归属由中心数据面依据已验证的写入身份 + Registry 映射决定。
- 若跨集群网络/mTLS 在 acceptance 环境不可行，则**收缩 Gate** 到：event + 明确受控 OTLP generator（见 §8.1），不引入不可信裸写入。

## 4. [P1.1] 未授权结果固定 403（不写 NO_DATA/403）
- tenant/cluster/capability 不匹配 → **一律 403**（`permission_denied`），不是 `NO_DATA`。
- 仅当**已授权**且查询结果为空 → `no_data`。
- Gate 通过 403 证明**身份边界**（而非碰巧无数据）；通过 `no_data` 证明已授权但无数据。

## 5. [P1.2] 回滚 retire，不删除身份记录
- kind-02 数据一旦写入中心存储，**删除 clusters/tenant_clusters 会让历史 Evidence/RCA 失去可解释身份**。
- 回滚应：
  1. 停止 kind-02 collector（scale 0 / 卸载采集）。
  2. 撤销跨集群凭据（revoke SA token + 删 ClusterRoleBinding）。
  3. **Cluster Registry 置 `lifecycle_status=retired`**（保留 `clusters`/`tenant_clusters` 记录，保留审计）。
  4. **保留历史数据**（按既定数据保留期），不删除 kind-02 已写入的 Evidence/RCA。
- 回滚触发：跨集群数据污染 / 隔离被破坏 / 主集群性能受影响。

## 6. [P1.3] 可复现的跨集群 RCA 场景
**受控 telemetry 不足以证明未串扰。固定以下场景：**
- 两集群部署**同名服务**（如 `order-svc` 同时在 orbstack 与 kind-02）。
- 注入**不同错误指纹**：orbstack `order-svc` 注入 metric anomaly（如 error-rate 峰值）；kind-02 `order-svc` 注入**不同类型**异常（如 OOMKilled / 不同 log pattern）。
- **双向各执行一次** RCA，断言：
  - orbstack `order-svc` RCA 的 **Evidence/Hypothesis/结果只含 `cluster_id=91771a6e-...`**。
  - kind-02 `order-svc` RCA 的 **Evidence/Hypothesis/结果只含 kind-02 cluster_id**。
  - 两者**互不串扰**（不含对方 cluster 的证据/结论）。

## 7. [P1.4] otel-collector 固定 / CH 表 owner / 非 canonical tenant / 速率与容量 / K8sGPT 移出

### 7.1 otel-collector 固定为必选（指标/日志属于 Gate）
- 若 metrics/logs 属于隔离 Gate 的验证范围，则 **otel-collector 为必选**（非可选），且其写入走 §3 mTLS/服务身份信任链。
- 若 metrics/logs 不纳入 Gate，则 **收缩 Gate 到 event + 明确受控 OTLP generator**（不经 otel-collector）。
- **默认选择**：Gate 范围 = K8s 事件 + 受控 OTLP generator（指标/日志），**otel-collector 不作为本 Gate 必要组件**（避免跨集群 mTLS 复杂化）；指标/日志跨集群写入列为后续专项。此决策使 Gate 聚焦于 P19.3 隔离验证核心。

### 7.2 CH 表 / owner
| 数据源 | 中心 CH 表 | owner | 是否属已批准中心存储 |
|--------|-----------|-------|---------------------|
| K8s 事件 | `observability.k8s_events` | 中心数据面（event-collector→ingest）| ✅ |
| 告警 | `observability.alert_events` | 中心数据面 | ✅ |
| 日志 | `observability.log_records`（经 VM/VLogs 另存）| 中心数据面 | ✅ |
| 追踪 | `observability.trace_spans` | 中心数据面 | ✅ |
| 拓扑 | `observability.service_topology` | 中心数据面 | ✅ |
- **不引入 P14 已删除的 legacy CH writer**：kind-02 事件写 `k8s_events` 走**中心数据面现行 writer 链**（非 P14 删除的 legacy ClickHouse writer），复用主集群同一写入路径（`cluster_id` 标签区分）。

### 7.3 非 canonical 测试 tenant（tenant 隔离可复现）
- 建立**受控测试 tenant**（如 `p19-test-tenant`，UUID 固定）+ **受控测试用户**（`p19_tenant_user`，最小权限）+ **JWT**。
- 用于 tenant 隔离 Gate：用该测试 tenant 查 kind-02 数据 → **403**（未授权）；用 canonical tenant 查 → 授权。
- 测试后禁用该测试 tenant/用户。

### 7.4 telemetry 速率上限 / 主数据面容量观察与停止条件
- **速率上限**：受控 OTLP generator 与 kind-02 event-collector 写入速率设上限（如 events ≤ 50/s、metrics ≤ 100 点/s、logs ≤ 100 行/s）。
- **主数据面容量观察项**：监控 orbstack VM/VLogs/CH 的 CPU/内存/磁盘/查询延迟；kind-02 写入后对比基线。
- **停止条件**：主数据面任一容量指标超阈值（如 CH 磁盘 >80%、VM 查询 p95 翻倍）→ 立即停止 kind-02 写入，进入回滚。

### 7.5 K8sGPT 跨集群凭据移出必经 Gate
- K8sGPT 跨集群凭据验证（读 kind-02 的 `credential_ref`）**从本设计必经 Gate 中移出**。
- 待 K8sGPT 安全注入专项 + deepseek key 轮换完成后再纳入（P19.7 已实现安全注入，但跨集群 K8sGPT 凭据读取依赖中央 connector，属后续专项）。

## 8. 授权前提（Gate）— v0.2
实施需用户授权以下**只读**范围：
- **可执行（只读）**：
  1. kind-02 部署最小采集：`event-collector`（in-cluster SA）+ 受控 OTLP generator（事件/指标/日志，限速）。
  2. Cluster Registry 注册 kind-02（`clusters` + `tenant_clusters` + `credential_ref` 指向中央 `kubeconfig-kind-02` + `kubernetes_identity_uid`）。
  3. 建立中央 `kubeconfig-kind-02` Secret（永不过期 SA token，只读，经 cluster registry 引用）。
  4. 受控 telemetry 写入中心数据面（走 §3 信任链，mTLS/服务身份 + 服务端 cluster_id 校验）。
  5. 跨集群只读查询 + 隔离 Gate 复验 + §6 可复现跨集群 RCA（只读）。
- **禁止**：跨集群任何写操作/执行动作（F5 保持）；不在 kind-02 建第二套 VM/VLogs/CH；不引入 legacy CH writer；不改主集群 orbstack 现有数据/服务；不接入真实生产集群（仅 orbstack + kind-02）。
- **凭据最小权限**：kind-02 collector 用 in-cluster SA（本地）；中央跨集群访问仅 `k8sboundary-read`（只读）经 `credential_ref`；轮换用永不过期 SA token；撤销即 revoke。

## 9. 验收标准（只读 Gate）
1. kind-02 数据源可查询（经 query-api 按 kind-02 cluster_id 返回）。
2. **P19.3 隔离全项真实复验 PASS**：
   - tenant 不匹配 → **403**（非 no_data）。
   - cluster 不匹配 → **403**。
   - capability 不匹配 → **403**。
   - 已授权且空 → no_data（区分身份边界 vs 无数据）。
   - 跨集群污染 → 无（Evidence/Hypothesis/RCA 只含目标 cluster_id）。
3. §6 跨集群 RCA 场景双向执行，断言只含目标 cluster_id，互不串扰。
4. 回滚演练：kind-02 采集可停止 + 凭据撤销 + `lifecycle_status=retired`（保留身份记录与历史数据），主集群零影响。
5. 真实执行保持 NOT APPROVED。

## 10. 下一步
- v0.2 已落盘。评审通过后，授权**只读接入 + 隔离复验**。
- 实施顺序（只读）：①kind-02 最小采集（event-collector + 受控 OTLP generator）→ ②Cluster Registry 注册 → ③中央 `kubeconfig-kind-02` 凭据 → ④受控 telemetry（限速 + 信任链）→ ⑤隔离 Gate 复验（403/no_data 区分）→ ⑥§6 跨集群 RCA。
- K8sGPT 跨集群凭据验证移出本 Gate，待 P19.7 专项后纳入。
