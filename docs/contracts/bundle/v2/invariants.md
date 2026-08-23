# Contract Bundle v2 — Semantic Invariants

> 承载 JSON Schema 无法表达的跨字段/算法规则。三端 binding 必须实现并校验这些不变量。

## 1. Source Reliability V1（§三十六 固定表）
```text
metric_anomaly=0.95, k8s_state=0.95, trace_anomaly=0.90, k8s_event=0.90, change=0.90,
topology_relation=0.85, resource_state=0.85, hardware_event=0.85, log_error=0.85,
log_pattern=0.80, capacity_anomaly=0.80, runbook=0.65, knowledge_case=0.60
```

## 2. provenance_fingerprint 算法
- 哈希输入**必须包含（固定顺序，NUL 分隔）**：`source, query_id, resource_id, time_range_start, time_range_end, digest, tenant_id, cluster_id, run_id`。
- 规范化：字段顺序固定（上述顺序，缺一不可，含 `time_range_end`）；时间归一化 ISO-8601 UTC（naive 时间拒绝，必须带时区，丢弃小数秒）；摘要编码 hex（SHA256）；UUID 字段小写规范；缺失值归一为 `""`（不得出现 "None"）。
- **跨租户/跨 cluster/跨 run → 必须不同 fingerprint**（防复用污染）。
- 对照实现：`ai-orchestrator/contracts_identity.py::canonical_provenance_fields`。三端必须复算一致。

## 3. Evidence Claim 规则
- claim_type=fact：仅允许真实数据源（VM/VLogs/query-api/MySQL/k8s）。
- claim_type=inference：必须带 supporting_evidence；禁 LLM 作为事实来源。
- 非法 source（LLM/Agent/未知）→ 拒绝（fail-closed），不得伪装 query-api。

## 4. Hypothesis confirmed 条件（§四十）
```text
final_score >= 0.80
AND >=1 direct evidence reliability >= 0.85
AND no unresolved critical contradiction
AND no critical missing
```

## 5. 预算消耗规则（PlannerState）
- 顺序：每 step 先检查 `consumed_steps + cost <= max_steps`，再 `consumed_tools <= max_tools`，再 `consumed_latency <= max_latency`。
- 超限 → fail-closed（拒绝该 step）。

## 6. V1/V2 兼容
- V2 只加不改：新增字段带默认值 / 新增枚举值不改变既有序列化值。
- bundle_version 与 payload wire_version 分离；不写 schema_version 到已签名 Context。
