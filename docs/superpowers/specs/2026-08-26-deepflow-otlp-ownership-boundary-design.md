# DeepFlow OTLP Ownership Boundary Design

**Date:** 2026-08-26

## Goal

将 AIOps 与 DeepFlow 的数据职责收敛到最终架构：保留两套物理 ClickHouse，DeepFlow 只负责自己的原始数据，AIOps 只通过官方 OTLP/gRPC exporter 接收 DeepFlow Span 并写入平台 ClickHouse。

## Current evidence

- 当前 Ingest Deployment 注入 `DEEPFLOW_CH_HOST`、`DEEPFLOW_CH_PORT`。
- `ai-apm-ingest-go/internal/pipeline/deepflow_sync.go` 通过 HTTP 查询 DeepFlow ClickHouse，并包含 `ALTER TABLE ... MODIFY TTL`。
- 当前 Ingest Service 只暴露 8080；自研 Ingest 没有 OTLP/gRPC 4317 接收端。
- 已部署 DeepFlow `server.yaml` 的 `ingester` 没有 `exporters` 配置。
- DeepFlow 官方 OpenTelemetry Exporter 支持 `flow_log.l7_flow_log` → OTLP/gRPC，目标端点只支持 gRPC 形式的 `host:port`：<https://www.deepflow.io/docs/integration/output/export/opentelemetry-exporter/>

## Target architecture

```text
DeepFlow Agent
    -> DeepFlow Server
    -> DeepFlow-owned ClickHouse
       flow_log / flow_metrics / flow_tag

DeepFlow Server official OTLP exporter
    -> ingest.observability:4317
    -> normalize / validate / tenant-tag / deduplicate
    -> platform ClickHouse observability.trace_spans
    -> Query API / topology / evidence / RCA
```

### Ownership invariants

1. DeepFlow ClickHouse is owned and mutated only by DeepFlow.
2. AIOps runtime has no DeepFlow ClickHouse hostname, SQL client, read path, or `ALTER TABLE` path.
3. `observability.trace_spans` is the platform Trace source of truth.
4. VictoriaLogs remains the platform log source of truth; `observability.log_records` is not populated from DeepFlow flow logs.
5. The original DeepFlow raw rows remain queryable through DeepFlow-owned tooling and are acceptance evidence, not an AIOps runtime dependency.
6. No DeepFlow source fork or source modification is allowed.

## Receiver contract

The Ingest process keeps the existing HTTP OTLP/JSON endpoint for existing SDK clients and adds an OTLP/gRPC TraceService endpoint on a separate port:

- Listen: `OTLP_GRPC_PORT`, default `4317`.
- Service: `ingest` exposes TCP 4317 to the observability namespace.
- Request: `ExportTraceServiceRequest` from `opentelemetry-proto`.
- Tenant: require lowercase gRPC metadata `x-tenant-id`; it must equal the configured `DEEPFLOW_TENANT_ID`.
- Failure: missing/mismatched tenant returns gRPC `Unauthenticated`; malformed or unprocessable data returns a non-OK status; no successful ACK is returned before all spans are accepted by the platform Span sink.
- Conversion: resource attributes provide `service.name`, `service.instance.id`, `k8s.namespace.name`, and `k8s.pod.name`; span attributes map to the existing internal `model.Span` fields and preserve trace/span/parent IDs.
- Metrics: expose counters for received, accepted, rejected-tenant, malformed, and sink-failed OTLP gRPC batches.

The JSON and gRPC paths call the same internal conversion and persistence routine so that status/error semantics do not diverge.

## DeepFlow configuration contract

`deploy/helm/aiops/values-deepflow.yaml` adds the chart-supported nested `configmap.server.yaml.ingester.exporters` entry:

```yaml
configmap:
  server.yaml:
    ingester:
      exporters:
        - protocol: opentelemetry
          enabled: true
          endpoints:
            - ingest.observability.svc.cluster.local:4317
          data-sources:
            - flow_log.l7_flow_log
          batch-size: 32
          flush-timeout: 10
          export-fields:
            - $tag
            - $metrics
            - $k8s.label
          extra-headers:
            x-tenant-id: 7ed01afc-cc79-4ecd-8767-a2befa6168ad
```

The literal tenant ID is routing identity, not a secret. No provider key, ClickHouse password, or Grafana credential is added to this configuration.

## Migration and rollback

The migration is deliberately staged:

1. Deploy the gRPC receiver and exporter configuration while the legacy syncer remains enabled.
2. Verify the receiver counter increases, newly received spans contain DeepFlow exporter attributes, and platform ClickHouse has recent rows.
3. Observe at least two exporter flush intervals while DeepFlow raw row counts continue increasing.
4. Disable the legacy syncer and remove its environment variables and NetworkPolicy exception.
5. Rebuild, deploy, and verify the final state has no direct DeepFlow ClickHouse runtime path.

If OTLP data does not arrive, stop before step 4 and restore only the staged exporter/receiver deployment; do not delete DeepFlow data and do not claim the final architecture is complete.

## Acceptance evidence

- Rendered DeepFlow manifest contains `ingester.exporters.protocol=opentelemetry`, the correct internal endpoint, and only `flow_log.l7_flow_log`.
- Ingest manifest exposes 4317 and no longer exposes a DeepFlow ClickHouse environment variable after cutover.
- Unit tests cover gRPC conversion, tenant rejection, malformed requests, sink failure, and shared JSON/gRPC conversion.
- Runtime metric shows accepted OTLP gRPC batches.
- Platform `observability.trace_spans` has fresh rows from the OTLP path.
- DeepFlow-owned `flow_log.l7_flow_log` has fresh rows independently of AIOps.
- AIOps pods remain Ready after the legacy syncer is removed.
- Repository HEAD, deployed image tags, and `origin/main` resolve to the same Git SHA.

## Non-goals

- Do not merge the two ClickHouse instances.
- Do not make AIOps query DeepFlow ClickHouse at runtime.
- Do not convert DeepFlow flow logs into `observability.log_records`.
- Do not enable DeepSeek AI Chat or send real telemetry to an external model as part of this migration.
