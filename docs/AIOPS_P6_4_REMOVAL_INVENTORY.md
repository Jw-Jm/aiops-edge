# AIOps V9.2 Phase 6 — P6.4.4 Adapter / Config Removal Inventory

> 状态：PREPARED（P6.5 才执行删除）
> 约束：**P6.4.4 只准备 removal inventory，不提前删除生产 fallback。**
> 真正删除顺序在 P6.5：`fresh verification PASS → old writer stopped → old reader stopped → then remove adapters/fallback`。

## 目标终态（Gate 6 后）

```
new writer ACTIVE
new reader ACTIVE
old writer ABSENT
old reader ABSENT
old active adapter ABSENT
production fallback ABSENT
old physical historical data PRESENT BUT UNREACHABLE
```

## Removal Inventory（按域，P6.5 删除）

### 1. Old writer adapter（ingest）
- [ ] `ai-apm-ingest-go` legacy ClickHouse 写路径（span/edge/log → ClickHouse `WAL` + batch insert）
- [ ] legacy writer 的 `ModeLegacy` 生产写激活路径
- [ ] 移除后保留：new VM/VLogs writer（真实 HTTP 传输）为唯一生产写 SoT

### 2. Old reader adapter（query）
- [ ] `queryClickHouse` / `parseRows`（已在 P6.2 删除为 legacy generic helper ABSENT）
- [ ] legacy reader 的 ClickHouse-only 查询路径（metrics/logs 的 legacy 分支）

### 3. ReaderMode legacy/new router + transition
- [ ] `SourceRouter` 的 `legacy` 分支（仅保留 `new`）
- [ ] `QUERY_READER_MODE` 的 `legacy` 值（TRANSITION_ONLY，Gate 6 后移除）
- [ ] `QUERY_READER_MODE` 环境变量本身（REMOVE_BEFORE_GATE6）
- [ ] `ReaderMode` legacy 枚举语义（若不再需要 transition）

### 4. Fallback code
- [ ] metrics/logs 的 `new mode 失败 → legacy fallback` 路径（静态搜索确认无，见 P6.3.5）
- [ ] 任何 `victoriaMetrics down → queryClickHouse` 类回退（生产 fallback ABSENT）

### 5. Legacy env / config
- [ ] ClickHouse 作为 raw metrics/raw logs 事实来源的配置（raw 已切 VM/VLogs SoT）
- [ ] 旧 ClickHouse 连接作为 raw 查询目标的环境变量

### 6. Legacy Helm value
- [ ] `values.yaml` `queryReaderMode: "legacy"`（Gate 6 后删除该 value 或强制 `new`）
- [ ] 旧 raw-storage 相关的 Helm 配置

### 7. Dead tests
- [ ] 依赖 legacy reader / ClickHouse raw 事实来源的测试（P6.5 同步清理或重定向到 new backend）

## 例外
除非发现真实 architecture conflict 或无法满足 Gate 的环境 blocker，否则 P6.5 严格按上述 inventory 删除，不得提前删生产 fallback。
