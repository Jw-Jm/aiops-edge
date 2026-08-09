# ClickHouse TabSeparated 转义缺陷修复（DeepFlow 落库）

**日期**: 2026-08-09
**性质**: 设计文档（bug 修复，方向已与用户确认）
**目标**: 修复 ingest 写入 ClickHouse 的 TabSeparated 序列化**缺失 `\t`/`\n`/`\\` 转义**的缺陷，使 DeepFlow（及 OTLP/loadgen）数据能稳定落库，消除 `Cannot parse input` 400 错误。
**落点**: ai-apm-ingest-go 的 `internal/clickhouse/writer.go` 与 `internal/clickhouse/log_writer.go`。

---

## 0. 问题与根因

**现象**：ingest 持续报 400 错误：
```
clickhouse error 400: Cannot parse input: expected '\t' before: '-08-07\ndefault\tdeepflow-agent\tkube-dns\t2026-08-07 11:01:00\t40\t0\t0...'
Column 1, name: trace_id, type: String, parsed text: "deepflow-grafana"
```
数据拉了但写不进 ClickHouse，只能反复重试。

**根因**：`Writer.serializeSpans`（trace_spans）与 `LogWriter.insertBatch`（log_records）拼接 **TabSeparated 行时，对文本字段完全没有转义 `\t`/`\n`/`\\`/`\r`**。当字段（如 DeepFlow `request_resource` 请求路径、日志 `Body`、`ServiceName`）含换行/制表符时，行被拆裂、字段错位，ClickHouse 解析失败。

---

## 1. 修复范围

### 1.1 新增转义 helper
在 `internal/clickhouse/writer.go`（或独立 `escape.go`）新增：
```go
// escapeTSV 按 ClickHouse TabSeparated 格式转义字段内容，防止含 \t \n \r \\ 时行被拆裂/错位
func escapeTSV(s string) string {
	if !strings.ContainsAny(s, "\\\t\n\r") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
```

### 1.2 应用转义到 serializeSpans（writer.go）
对 `serializeSpans` 里写入 TSV 的所有**文本字段**包一层 `escapeTSV`：
- `TenantID`、`ServiceName`、`OperationName`、`SpanKind`、`TraceID`、`SpanID`
- `Attributes` 的每个 key/value（`escapeCH` 已转义 `'`，但还需 TSV 转义）
- `HTTPMethod`、`HTTPURL`、`DBSystem`、`DBStatement`
- `ServiceInstanceID`、`K8sNamespace`、`K8sPodName`

数字/时间/状态码字段（`DurationMs`、`TimeBucket`、`Date`、`StatusCode`、`HasError`）无需转义（数值/固定格式）。

注意：`DBStatement` 是最易含换行的字段（SQL 语句可跨行），必须转义。

### 1.3 应用转义到 insertBatch（log_writer.go）
对 `insertBatch` 里写入 TSV 的所有**文本字段**包 `escapeTSV`：
- `TenantID`、`ServiceName`、`Severity`、`Body`（日志正文最易含换行）、`TraceID`、`SpanID`
- `Attributes` 的 key/value（在 `escapeCH` 基础上追加 TSV 转义）

时间/数字字段（Timestamp、TimeBucket、Date）无需转义。

### 1.4 不修改范围
- `escapeCH`（转义 Map 内部 `'`）保持不变，Map 的 attrStr 作为整体字段，其内部已有引号，**整个 attrStr 需再套一层 escapeTSV**（因为 attrStr 可能含 Map 值里的换行）。
- 不改 DeepFlowSyncer 拉取逻辑（拉取正常）、不改表结构、不改其他格式。

---

## 2. 测试（TDD）

新增 `internal/clickhouse/writer_test.go`：
- `TestEscapeTSV`：验证 `\\`→`\\\\`、`\t`→`\t`(转义)、`\n`→`\n`(转义)、`\r`、混合、无特殊字符原样返回
- `TestSerializeSpansEscapesNewlines`：构造含 `\n`/`\t` 的 span（如 `DBStatement: "SELECT 1\nFROM t"`），调用 serializeSpans，断言输出行**不再含裸 `\n`/`\t` 拆裂**（即每行以转义后的 `\n` 结尾且字段内无裸制表符）
- `TestLogInsertBatchEscapes`：构造含换行的 Body，断言 insertBatch 输出正确转义

验证方式：转义后，每行的列数（按 TabSeparated 预期）稳定；ClickHouse 能解析。

---

## 3. 数据/合规
- 全自研 bug 修复，无新依赖、无新表
- 改动仅 2 个写入器 + 1 个 helper，组件最小化
- 修复后 DeepFlow/OTLP/loadgen 数据稳定落库

## 4. 自审
- [x] 根因定位（TSV 转义缺失）
- [x] serializeSpans + insertBatch 均补转义
- [x] escapeTSV 按 ClickHouse TabSeparated 规则（\\ \t \n \r）
- [x] 单元测试覆盖
- [x] 不改动拉取/表结构，最小化
