# AIOps 本机 K8s 部署 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 macOS(arm64)+OrbStack K8s 上，用 Helm Chart 一键部署自研 AIOps 平台（4 服务 + 全部中间件 + deepflow 完整装），除副本数=1 外严格按生产标准；并完成 2 处代码改动（LLM mock 通道、DeepFlow 实时增量同步）。

**Architecture:** 一个 parent Helm Chart（`deploy/helm/aiops/`）编排全部组件；各中间件/自研服务用 `enabled`+`external` 可插拔；命名空间 `observability`/`deepflow` 与代码硬编码 DNS 精确对齐；ClickHouse/MySQL 由 InitContainer 自动建表；deepflow 用官方子 chart 依赖。代码改动集中在 orchestrator（LLM mock）与 ingest（增量同步）。

**Tech Stack:** Helm v3.19 / Go 1.24 / Python 3.12 / Node 20 / ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / ChromaDB / MinIO / MySQL / deepflow chart / OrbStack K8s (arm64)。

## Global Constraints

- 镜像架构：**arm64**（本机 OrbStack），自研服务 `docker build` 默认 arm64；中间件用官方 arm64 镜像。
- 副本数：**全部 = 1**（唯一偏离生产标准）。
- 命名空间：**照建 `observability` + `deepflow`**。
- 存储类：PVC `storageClass: ""`（用集群默认 `local-path`，可移植改 SC 名）。
- 密钥：全部走 values 注入的 Secret，**不硬编码、不进 git**（`.gitignore` 已排除 `.env`/`secrets/`）。
- LLM：默认 **mock**（`LLM_MOCK=true`），后续配真实 Key 只改 values。
- 端口：前端 NodePort **30253**。
- 实时同步：`DEEPFLOW_SYNC_INTERVAL`（默认 60s）+ 增量拉取（`lastSyncTime`）。
- 合规：`ongrid-ref/`（AGPL）已被 `.gitignore` 排除，**永不入库**。
- 代码硬编码 DNS（Helm Service 名必须精确匹配，否则服务连不上）：
  - `clickhouse-0.clickhouse.observability.svc.cluster.local` / `clickhouse.observability.svc.cluster.local` / `redis.observability.svc.cluster.local` / `victoria-logs.observability.svc.cluster.local` / `victoria-metrics.observability.svc.cluster.local` / `minio.observability.svc.cluster.local` / `query-api.observability.svc.cluster.local`
  - orchestrator 硬编码路径：ChromaDB `/tmp/ops-cases`、LangGraph checkpoint `/tmp/ai-sessions.db`（挂 PVC 到对应路径）
- 基线已推送 `github.com/Jw-Jm/aiops-edge`（main=8514e80），每任务提交。

---

## Task 1: orchestrator 增加 LLM mock 通道

**Files:**
- Modify: `aiops/ai-orchestrator/main.py`
- Modify: `aiops/ai-orchestrator/orchestrator.py`
- Test: `aiops/ai-orchestrator/tests/test_llm_mock.py`

**Interfaces:**
- Consumes: 现有 `set_llm_config()`（orchestrator.py，写 `OPENAI_*` env）与 LLM 调用入口。
- Produces: env `LLM_MOCK`（`"true"`/`"1"` 视为开启）→ 调用链短路返回预设 mock 响应，不访问真实模型；`main.py`/`server.py` 启动时读取并设置全局开关。

- [ ] **Step 1: 写失败测试**

```python
# aiops/ai-orchestrator/tests/test_llm_mock.py
import os
import pytest
from orchestrator import is_mock_enabled, mock_llm_response

def test_mock_disabled_by_default(monkeypatch):
    monkeypatch.delenv("LLM_MOCK", raising=False)
    assert is_mock_enabled() is False

def test_mock_enabled_when_true(monkeypatch):
    monkeypatch.setenv("LLM_MOCK", "true")
    assert is_mock_enabled() is True

def test_mock_response_shape():
    resp = mock_llm_response("who is the caller?")
    assert isinstance(resp, str) and len(resp) > 0
    assert "RCA" in resp or "analysis" in resp.lower()
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-orchestrator && python3 -m pytest tests/test_llm_mock.py -v`
Expected: FAIL with `ModuleNotFoundError`/`ImportError`（`is_mock_enabled`/`mock_llm_response` 未定义）。

- [ ] **Step 3: 实现 mock 通道**

在 `orchestrator.py` 增加：
```python
import os

def is_mock_enabled() -> bool:
    return os.getenv("LLM_MOCK", "").lower() in ("true", "1", "yes")

def mock_llm_response(prompt: str) -> str:
    """LLM mock：返回预设诊断文本，便于界面联调，不消耗真实模型。"""
    return (
        "[mock] 已生成根因分析：从指标与拓扑看，可能为最近一次发布引起的调用异常。\n"
        f"待分析内容：{prompt[:200]}"
    )
```
并在 LLM 调用入口（`BrainOrchestrator` 中触发 LLM 的路径）加短路：
```python
if is_mock_enabled():
    return mock_llm_response(prompt)
```
`main.py` 启动时 `os.environ.setdefault("LLM_MOCK", os.getenv("LLM_MOCK", "true"))` 默认开启 mock。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-orchestrator && python3 -m pytest tests/test_llm_mock.py -v`
Expected: PASS（3 tests）。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-orchestrator/orchestrator.py ai-orchestrator/main.py ai-orchestrator/tests/test_llm_mock.py
git commit -m "feat(orchestrator): add LLM mock channel via LLM_MOCK env"
```

---

## Task 2: ingest 的 DeepFlowSyncer 支持可配置间隔 + 增量拉取

**Files:**
- Modify: `aiops/ai-apm-ingest-go/internal/pipeline/deepflow_sync.go`
- Modify: `aiops/ai-apm-ingest-go/cmd/ingest/main.go`
- Test: `aiops/ai-apm-ingest-go/internal/pipeline/deepflow_sync_test.go`

**Interfaces:**
- Consumes: 现有 `DeepFlowSyncer`、`Sync()`、`syncTraces()`、`queryDF()`（保留签名）。
- Produces: `DeepFlowSyncer.interval` 从 env `DEEPFLOW_SYNC_INTERVAL`（默认 `60s`，合法范围 `5s`–`3600s`，非法回退默认）读取；新增 `lastSyncTime time.Time` + `lastSyncMu sync.Mutex`；`Sync()` 增量拉取（`startTime` 用 `lastSyncTime`，结束后更新）。

- [ ] **Step 1: 写失败测试**

```go
// aiops/ai-apm-ingest-go/internal/pipeline/deepflow_sync_test.go
package pipeline

import (
	"testing"
	"time"
)

func TestParseSyncInterval_Default(t *testing.T) {
	if got := parseSyncInterval(""); got != 60*time.Second {
		t.Fatalf("default interval = %v, want 60s", got)
	}
}

func TestParseSyncInterval_Valid(t *testing.T) {
	if got := parseSyncInterval("10s"); got != 10*time.Second {
		t.Fatalf("got %v, want 10s", got)
	}
}

func TestParseSyncInterval_ClampedOutOfRange(t *testing.T) {
	if got := parseSyncInterval("1h"); got != 60*time.Second { // >3600s 回退默认
		t.Fatalf("too-large interval = %v, want default", got)
	}
	if got := parseSyncInterval("1s"); got != 60*time.Second { // <5s 回退默认
		t.Fatalf("too-small interval = %v, want default", got)
	}
}

func TestParseSyncInterval_InvalidString(t *testing.T) {
	if got := parseSyncInterval("abc"); got != 60*time.Second {
		t.Fatalf("invalid interval = %v, want default", got)
	}
}

func TestClampStartTime_NoClockSkew(t *testing.T) {
	now := time.Now().UTC()
	last := now.Add(-2 * time.Minute)
	if got := clampStartTime(last, now); got.Unix() != last.Unix() {
		t.Fatalf("clamp changed valid last time: %v", got)
	}
}

func TestClampStartTime_FutureOrTooOld(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(time.Hour), now); got.After(now) {
		t.Fatalf("clamp did not pull future time back: %v", got)
	}
	if got := clampStartTime(now.Add(-2*time.Hour), now); got.Before(now.Add(-15*time.Minute)) {
		t.Fatalf("clamp allowed too-old start: %v", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-ingest-go && go test ./internal/pipeline/ -run "TestParseSyncInterval|TestClampStartTime" -v`
Expected: FAIL（`parseSyncInterval`/`clampStartTime` 未定义，编译失败）。

- [ ] **Step 3: 实现**

在 `deepflow_sync.go` 增加：
```go
import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSyncInterval = 60 * time.Second
	minSyncInterval     = 5 * time.Second
	maxSyncInterval     = 3600 * time.Second
)

// parseSyncInterval 从 env DEEPFLOW_SYNC_INTERVAL 解析同步间隔；非法/越界回退默认。
func parseSyncInterval(v string) time.Duration {
	if v == "" {
		return defaultSyncInterval
	}
	// 支持 "30" (秒) 或 "30s" (Go duration)
	s := strings.TrimSpace(v)
	if secs, err := strconv.Atoi(s); err == nil {
		s = s + "s"
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultSyncInterval
	}
	if d < minSyncInterval || d > maxSyncInterval {
		return defaultSyncInterval
	}
	return d
}

// clampStartTime 处理时钟回拨/漂移，把增量起点限制在 [now-15m, now] 内。
func clampStartTime(last, now time.Time) time.Time {
	lo := now.Add(-15 * time.Minute)
	if last.Before(lo) {
		return lo
	}
	if last.After(now) {
		return now
	}
	return last
}
```
在 `DeepFlowSyncer` struct 增加字段：
```go
	lastSyncMu sync.Mutex
	lastSyncTime time.Time
```
`NewDeepFlowSyncer` 增加读取：
```go
	s.interval = parseSyncInterval(os.Getenv("DEEPFLOW_SYNC_INTERVAL"))
```
在 `Sync()` 开头改为增量窗口：
```go
	now := time.Now().UTC()
	s.lastSyncMu.Lock()
	start := clampStartTime(s.lastSyncTime, now)
	s.lastSyncMu.Unlock()
	// 用 start 替换原固定 "now() - INTERVAL 10 MINUTE" 的查询窗口
	windowStart := start.Add(-1 * time.Minute) // 保护重叠，避免漏拉
```
`Sync()` 末尾更新 `lastSyncTime`：
```go
	s.lastSyncMu.Lock()
	s.lastSyncTime = now
	s.lastSyncMu.Unlock()
```
`cmd/ingest/main.go` 装配处：若未设置 `DEEPFLOW_CH_HOST` 则禁用同步器（现状保持），启动后日志打印实际间隔。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-ingest-go && go test ./internal/pipeline/ -run "TestParseSyncInterval|TestClampStartTime" -v`
Expected: PASS（6 tests）。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-ingest-go/internal/pipeline/deepflow_sync.go ai-apm-ingest-go/cmd/ingest/main.go ai-apm-ingest-go/internal/pipeline/deepflow_sync_test.go
git commit -m "feat(ingest): DeepFlowSyncer configurable interval + incremental sync"
```

---

## Task 3: Helm Chart 骨架 + 命名空间 + 全局 values

**Files:**
- Create: `aiops/deploy/helm/aiops/Chart.yaml`
- Create: `aiops/deploy/helm/aiops/values.yaml`
- Create: `aiops/deploy/helm/aiops/values-prod.yaml`
- Create: `aiops/deploy/helm/aiops/requirements.yaml`
- Create: `aiops/deploy/helm/aiops/templates/namespaces.yaml`
- Create: `aiops/deploy/helm/aiops/templates/_helpers.tpl`

**Interfaces:**
- Consumes: spec §3.1 结构、§10 env 清单。
- Produces: parent chart 骨架；`values.yaml` 定义全部组件 `enabled`/`external`/`image`/`storageClass`/`resources`/`secrets.*` 占位；`namespaces.yaml` 声明 `observability`/`deepflow`。

- [ ] **Step 1: 写 Chart.yaml**

```yaml
apiVersion: v2
name: aiops
description: AIOps 平台（自研 ongrid 风格）本机/生产 Helm Chart
type: application
version: 0.1.0
appVersion: "1.0.0"
dependencies:
  - name: deepflow
    version: "6.5.0"
    repository: "https://deepflowio.github.io/deepflow"
    condition: deepflow.enabled
```

- [ ] **Step 2: 写 values.yaml（核心参数）**

```yaml
# 全局
replicaCount: 1
namespace:
  observability: observability
  deepflow: deepflow

# secrets（占位，实际用 values 文件/--set 注入，不提交真实值）
secrets:
  jwtSecret: ""
  internalToken: ""
  ingestApiKey: ""
  clickhousePassword: ""
  redisPassword: ""
  minioAccessKey: minioadmin
  minioSecretKey: minioadmin123
  mysqlRootPassword: ""

# 各中间件：enabled=true 内部拉起；enabled=false 走 external
clickhouse:
  enabled: true
  external: { host: "", port: "8123" }
  image: "clickhouse/clickhouse-server:24.8-alpine"
  storageClass: ""
  storage: 20Gi
victoriaMetrics:
  enabled: true
  image: "victoriametrics/victoria-metrics:v1.101.0"
  storageClass: ""
  storage: 5Gi
victoriaLogs:
  enabled: true
  image: "victoriametrics/victoria-logs:v1.6.0"
  storageClass: ""
  storage: 5Gi
redis:
  enabled: true
  image: "redis:7-alpine"
  storageClass: ""
  storage: 1Gi
chromadb:
  enabled: true
  image: "chromadb/chroma:0.5.5"
  storageClass: ""
  storage: 5Gi
minio:
  enabled: true
  image: "minio/minio:RELEASE.2024-09-13T20-26-02Z"
  storageClass: ""
  storage: 10Gi
mysql:
  enabled: true
  image: "mysql:8.4"
  storageClass: ""
  storage: 10Gi
vmalert:
  enabled: true
  image: "victoriametrics/vmalert:v1.101.0"

# 自研服务（镜像 tag 由 build-images.sh 决定，统一用 :latest 本地构建）
frontend:
  enabled: true
  image: "docker.io/library/observability-frontend:latest"
  nodePort: 30253
queryApi:
  enabled: true
  image: "docker.io/library/query-api:latest"
  clickhouseHost: "clickhouse-0.clickhouse.observability.svc.cluster.local"
  clickhousePort: "8123"
  redisUrl: "redis.observability.svc.cluster.local:6379"
  victoriaLogsUrl: "http://victoria-logs.observability.svc.cluster.local:9428"
ingest:
  enabled: true
  image: "docker.io/library/ingest-pipeline:latest"
  clickhouseHost: "clickhouse.observability.svc.cluster.local"
  clickhousePort: "8123"
  deepflowChHost: "deepflow-clickhouse.deepflow.svc.cluster.local"
  deepflowChPort: "8123"
  syncInterval: "10s"
aiOrchestrator:
  enabled: true
  image: "docker.io/library/ai-orchestrator:latest"
  queryApiUrl: "http://query-api.observability.svc.cluster.local:8080/api/v1"
  minioEndpoint: "minio.observability.svc.cluster.local:9000"
  clickhouseHost: "clickhouse-0.clickhouse.observability.svc.cluster.local"
  llmMock: "true"

deepflow:
  enabled: true
```

- [ ] **Step 3: 写 namespaces.yaml 与 _helpers.tpl**

```yaml
# templates/namespaces.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Values.namespace.observability }}
---
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Values.namespace.deepflow }}
```
```gotmpl
# templates/_helpers.tpl
{{- define "aiops.name" -}}{{ .Chart.Name }}{{- end -}}
{{- define "aiops.fullname" -}}{{ .Release.Name }}-{{ .Chart.Name }}{{- end -}}
{{- define "aiops.ns" -}}{{ .Values.namespace.observability }}{{- end -}}
```

- [ ] **Step 4: 校验 chart 可渲染**

Run: `cd aiops/deploy/helm/aiops && helm template . --namespace observability 2>&1 | head -40`
Expected: 输出 Namespace 资源，无 template 报错。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add deploy/helm/aiops
git commit -m "feat(helm): chart skeleton, namespaces, global values"
```

---

## Task 4: 基础中间件模板（ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / ChromaDB / MinIO / MySQL / vmalert）

**Files:**
- Create: `aiops/deploy/helm/aiops/templates/clickhouse/statefulset.yaml`, `service.yaml`, `pvc.yaml`, `init-configmap.yaml`
- Create: `aiops/deploy/helm/aiops/templates/victoria-metrics/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/victoria-logs/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/redis/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/chromadb/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/minio/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/mysql/*.yaml`
- Create: `aiops/deploy/helm/aiops/templates/vmalert/*.yaml`

**Interfaces:**
- Consumes: Task 3 values；`files/clickhouse/init_clickhouse.sql`（已存在）。
- Produces: 各中间件 Deployment/StatefulSet + Service + PVC；ClickHouse/MySQL 的 InitContainer 自动建表。

- [ ] **Step 1: ClickHouse StatefulSet + InitContainer 建表**

每个有状态组件用 `<component>-0` 命名 StatefulSet 以匹配硬编码 DNS。ClickHouse 示例：
```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: clickhouse
  namespace: {{ .Values.namespace.observability }}
spec:
  serviceName: clickhouse
  replicas: {{ .Values.replicaCount }}
  selector: { matchLabels: { app: clickhouse } }
  template:
    metadata: { labels: { app: clickhouse } }
    spec:
      initContainers:
        - name: init-tables
          image: {{ .Values.clickhouse.image }}
          command: ["/bin/sh","-c"]
          args:
            - "until clickhouse-client --host 127.0.0.1 --query 'SELECT 1' >/dev/null 2>&1; do sleep 2; done; clickhouse-client --multiquery < /init/init_clickhouse.sql"
          volumeMounts:
            - name: init-sql
              mountPath: /init
      containers:
        - name: clickhouse
          image: {{ .Values.clickhouse.image }}
          ports: [{ containerPort: 8123 }]
          volumeMounts:
            - name: data
              mountPath: /var/lib/clickhouse
          resources:
            requests: { cpu: "500m", memory: "1Gi" }
            limits: { cpu: "2", memory: "4Gi" }
      volumes:
        - name: init-sql
          configMap:
            name: clickhouse-init
  volumeClaimTemplates:
    - metadata: { name: data }
      spec:
        accessModes: [ "ReadWriteOnce" ]
        storageClassName: {{ .Values.clickhouse.storageClass | default "" | quote }}
        resources:
          requests: { storage: {{ .Values.clickhouse.storage }} }
```
- ConfigMap `clickhouse-init` 挂载 `files/clickhouse/init_clickhouse.sql`（`.Files.Get`）。
- ClickHouse Service（headless + ClusterIP，port 8123）命名 `clickhouse`（匹配 ingest 的 `clickhouse.observability...`）与 `clickhouse-0`（StatefulSet pod 名匹配 query/orchestrator 的 `clickhouse-0.clickhouse...`）。
- 其余中间件（VM/VL/Redis/Chroma/MinIO/MySQL/vmalert）同构模板，Service 名精确匹配 values（`victoria-metrics`/`victoria-logs`/`redis`/`minio`/`mysql`）。
- **MySQL**：官方镜像 + 版本化迁移 InitContainer。先建最小迁移文件 `files/mysql/migrations/0001_init.sql`：
```sql
-- 0001_init.sql: 业务状态库最小初始化（P1b 正式表结构前先建库）
CREATE DATABASE IF NOT EXISTS aiops;
USE aiops;
CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(255) PRIMARY KEY,
  applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT IGNORE INTO schema_migrations (version) VALUES ('0001_init');
```
InitContainer 命令：`until mysqladmin ping -h127.0.0.1 --silent; do sleep 2; done; mysql -h127.0.0.1 -uroot -p$MYSQL_ROOT_PASSWORD < /migrations/0001_init.sql`。`0001_init.down.sql` 预留（`DROP DATABASE IF EXISTS aiops;`）。

- [ ] **Step 2: 渲染校验所有模板**

Run: `cd aiops/deploy/helm/aiops && helm template . --namespace observability > /tmp/aiops-render.yaml 2>&1 && grep -c "^kind:" /tmp/aiops-render.yaml`
Expected: 输出多个资源 kind，无 template 报错。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add deploy/helm/aiops/templates deploy/helm/aiops/files
git commit -m "feat(helm): middleware templates with init db"
```

---

## Task 5: 自研服务模板（frontend / query-api / ingest / ai-orchestrator）+ Secret/ConfigMap + NodePort

**Files:**
- Create: `aiops/deploy/helm/aiops/templates/secrets.yaml`
- Create: `aiops/deploy/helm/aiops/templates/frontend/deployment.yaml`, `service.yaml`
- Create: `aiops/deploy/helm/aiops/templates/query-api/deployment.yaml`, `service.yaml`
- Create: `aiops/deploy/helm/aiops/templates/ingest/deployment.yaml`, `service.yaml`
- Create: `aiops/deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`, `service.yaml`

**Interfaces:**
- Consumes: Task 3 values、Task 4 Service DNS、env 清单（§Global Constraints）。
- Produces: 4 自研服务 Deployment + Service + NodePort（frontend 30253）；Secret 注入密钥；env 精确映射到硬编码 DNS。

- [ ] **Step 1: Secrets 模板**

```yaml
apiVersion: v1
kind: Secret
metadata: { name: aiops-secrets, namespace: {{ .Values.namespace.observability }} }
type: Opaque
stringData:
  JWT_SECRET: {{ .Values.secrets.jwtSecret | quote }}
  INTERNAL_TOKEN: {{ .Values.secrets.internalToken | quote }}
  INGEST_API_KEY: {{ .Values.secrets.ingestApiKey | quote }}
  CLICKHOUSE_PASSWORD: {{ .Values.secrets.clickhousePassword | quote }}
  REDIS_PASSWORD: {{ .Values.secrets.redisPassword | quote }}
  MINIO_ACCESS_KEY: {{ .Values.secrets.minioAccessKey | quote }}
  MINIO_SECRET_KEY: {{ .Values.secrets.minioSecretKey | quote }}
  MYSQL_ROOT_PASSWORD: {{ .Values.secrets.mysqlRootPassword | quote }}
```

- [ ] **Step 2: query-api Deployment（env 映射）

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: query-api
  namespace: {{ .Values.namespace.observability }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector: { matchLabels: { app: query-api } }
  template:
    metadata: { labels: { app: query-api } }
    spec:
      containers:
        - name: query-api
          image: {{ .Values.queryApi.image }}
          ports: [{ containerPort: 8080 }]
          env:
            - name: CLICKHOUSE_HOST
              value: {{ .Values.queryApi.clickhouseHost | quote }}
            - name: CLICKHOUSE_PORT
              value: {{ .Values.queryApi.clickhousePort | quote }}
            - name: REDIS_URL
              value: {{ .Values.queryApi.redisUrl | quote }}
            - name: JWT_SECRET
              valueFrom: { secretKeyRef: { name: aiops-secrets, key: JWT_SECRET } }
            - name: INTERNAL_TOKEN
              valueFrom: { secretKeyRef: { name: aiops-secrets, key: INTERNAL_TOKEN } }
          readinessProbe: { httpGet: { path: /healthz, port: 8080 }, initialDelaySeconds: 10 }
          livenessProbe: { httpGet: { path: /healthz, port: 8080 }, initialDelaySeconds: 20 }
          resources:
            requests: { cpu: "200m", memory: "512Mi" }
            limits: { cpu: "1", memory: "1Gi" }
```
- **ingest**：env 加 `CLICKHOUSE_HOST/PORT`、`DEEPFLOW_CH_HOST`（= `deepflow-clickhouse.deepflow.svc.cluster.local`，`{{ .Values.ingest.deepflowChHost }}`）、`DEEPFLOW_CH_PORT`、`DEEPFLOW_SYNC_INTERVAL`（= `{{ .Values.ingest.syncInterval }}`）、`INGEST_API_KEY`（Secret）、`INGEST_WAL_DIR=/wal`（挂 PVC）。
- **ai-orchestrator**：env 加 `QUERY_API_URL`、`INTERNAL_TOKEN`（Secret）、`MINIO_ENDPOINT/ACCESS_KEY/SECRET_KEY/SECURE`、`CLICKHOUSE_HOST/PORT`、`LLM_MOCK`；PVC 挂 `/tmp/ops-cases`（ChromaDB）、`/tmp/ai-sessions.db`（LangGraph）。
- **frontend**：Deployment 跑 nginx，Service 配 NodePort 30253；无需业务 env（nginx.conf 反代内置 DNS）。

- [ ] **Step 3: frontend Service NodePort**

```yaml
apiVersion: v1
kind: Service
metadata: { name: frontend, namespace: {{ .Values.namespace.observability }} }
spec:
  type: NodePort
  selector: { app: frontend }
  ports:
    - port: 80
      targetPort: 80
      nodePort: {{ .Values.frontend.nodePort }}
```

- [ ] **Step 4: 渲染校验**

Run: `cd aiops/deploy/helm/aiops && helm template . --namespace observability > /tmp/aiops-render.yaml 2>&1 && grep -c "kind: Deployment" /tmp/aiops-render.yaml`
Expected: ≥4 个 Deployment，无 template 报错。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add deploy/helm/aiops/templates
git commit -m "feat(helm): self-built service templates with secrets/nodeport"
```

---

## Task 6: deepflow 子 chart 依赖拉取与配置

**Files:**
- Create: `aiops/deploy/helm/aiops/values-deepflow.yaml`（deepflow 部分参数，agent DaemonSet 等）

**Interfaces:**
- Consumes: `Chart.yaml` dependencies（Task 3）。
- Produces: `helm dependency build` 拉取 deepflow chart；配置 deepflow-agent/server 在 `deepflow` namespace。

- [ ] **Step 1: 拉取依赖**

Run: `cd aiops/deploy/helm/aiops && helm repo add deepflow https://deepflowio.github.io/deepflow && helm repo update && helm dependency build`
Expected: 拉取 `deepflow-6.5.0.tgz` 到 `charts/`。

- [ ] **Step 2: 渲染含 deepflow**

Run: `helm template . --namespace observability --set deepflow.enabled=true > /tmp/aiops-render-full.yaml 2>&1 && grep -c "kind:" /tmp/aiops-render-full.yaml`
Expected: 输出包含 deepflow 资源，无报错。

- [ ] **Step 3: 提交（charts 目录通常不提交，加 .gitignore）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
grep -q "deploy/helm/aiops/charts/" .gitignore || echo "deploy/helm/aiops/charts/" >> .gitignore
git add .gitignore deploy/helm/aiops/values-deepflow.yaml
git commit -m "chore(helm): deepflow dependency config, ignore chart tarballs"
```

---

## Task 7: 构建脚本 + 一键部署脚本

**Files:**
- Create: `aiops/deploy/scripts/build-images.sh`
- Create: `aiops/deploy/scripts/apply.sh`
- Create: `aiops/deploy/scripts/destroy.sh`
- Create: `aiops/deploy/scripts/init-db.sh`

**Interfaces:**
- Consumes: 4 仓库 Dockerfile、Task 3-5 Chart。
- Produces: 本地构建 4 个 arm64 镜像并导入 OrbStack；helm install/upgrade；清理；手动建表逃生门。

- [ ] **Step 1: build-images.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# 构建 4 个自研服务镜像（arm64，本机 OrbStack）
build() {
  local dir="$1" name="$2"
  (cd "$ROOT/$dir" && docker build -t "docker.io/library/$name:latest" .)
  echo "[built] $name"
}
build observability-frontend observability-frontend
build ai-apm-query-go query-api
build ai-apm-ingest-go ingest-pipeline
build ai-orchestrator ai-orchestrator
echo "全部镜像构建完成。"
```

- [ ] **Step 2: apply.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail
CHART="$(cd "$(dirname "$0")/../helm/aiops" && pwd)"
helm dependency build "$CHART" >/dev/null 2>&1 || true
helm upgrade --install aiops "$CHART" \
  --namespace observability --create-namespace \
  --values "$CHART/values.yaml" \
  --set aiOrchestrator.llmMock="true" \
  --wait
echo "部署完成。NodePort: http://localhost:30253"
```

- [ ] **Step 3: destroy.sh（默认不删 PVC，--purge-data 才删）**

```bash
#!/usr/bin/env bash
set -euo pipefail
CHART="$(cd "$(dirname "$0")/../helm/aiops" && pwd)"
helm uninstall aiops --namespace observability
if [[ "${1:-}" == "--purge-data" ]]; then
  kubectl delete ns observability deepflow
  echo "已删除 namespace（含 PVC 数据）"
fi
```

- [ ] **Step 4: init-db.sh（手动建表逃生门）**

```bash
#!/usr/bin/env bash
set -euo pipefail
NS="${1:-observability}"
SQL_FILE="$(cd "$(dirname "$0")/../helm/aiops/files/clickhouse" && pwd)/init_clickhouse.sql"
kubectl -n "$NS" exec clickhouse-0 -- clickhouse-client --multiquery < "$SQL_FILE"
echo "已手动执行建表。"
```

- [ ] **Step 5: 加执行权限并提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
chmod +x deploy/scripts/*.sh
git add deploy/scripts
git commit -m "feat(scripts): build/apply/destroy/init-db scripts"
```

---

## Task 8: 本机端到端部署 + 验证

**Files:**
- 无新代码；执行部署与验证。

**Interfaces:**
- Consumes: Task 1-7 全部产物。

- [ ] **Step 1: 构建镜像**

Run: `cd aiops && ./deploy/scripts/build-images.sh`
Expected: 4 个镜像构建成功（arm64）。

- [ ] **Step 2: 部署**

Run: `cd aiops && ./deploy/scripts/apply.sh`
Expected: `helm upgrade --install` 成功，pods 就绪。

- [ ] **Step 3: 验证 Pod 状态**

Run: `kubectl -n observability get pods && kubectl -n deepflow get pods`
Expected: 4 自研服务 + 8 中间件 Running；deepflow 组件 Running。

- [ ] **Step 4: 验证建表（无历史数据）**

Run: `kubectl -n observability exec clickhouse-0 -- clickhouse-client --query "SELECT count() FROM system.tables WHERE database='observability'"`
Expected: 8（8 张表创建成功）；`SELECT count() FROM observability.trace_spans` = 0（无历史数据）。

- [ ] **Step 5: 验证前端可访问**

Run: `curl -s -o /dev/null -w "%{http_code}" http://localhost:30253/`
Expected: 200。

- [ ] **Step 6: 验证 AI mock**

Run: 浏览器访问 http://localhost:30253，进入 AI 聊天，发送任意查询。
Expected: 返回 mock 诊断文本（不消耗真实 LLM）。

- [ ] **Step 7: 验证实时同步（若 deepflow 有数据）**

Run: `kubectl -n observability logs deploy/ingest | grep DeepFlowSyncer`
Expected: 日志显示 `interval=10s` 且周期出现 `synced N edges`，无 `interval=60s` 默认（证明 DEEPFLOW_SYNC_INTERVAL 生效）。

- [ ] **Step 8: 提交验证通过（如有代码修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix(deploy): local deployment fixes after e2e verification" || echo "无改动"
```

---

## Task 9: 收尾——文档与推送基线

**Files:**
- Modify: `aiops/README.md`（如无则创建）

**Interfaces:**
- Consumes: 全部完成产物。

- [ ] **Step 1: 写 README（部署说明）**

创建 `aiops/README.md`：说明本机部署步骤（build-images.sh → apply.sh → 验证）、命名空间、NodePort、LLM mock 切换、实时同步 env、以及如何移植到其他环境（改 values 的 storageClass/external 地址）。

- [ ] **Step 2: 提交并推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add README.md
git commit -m "docs: add local k8s deployment README"
git push origin main
```

- [ ] **Step 3: 完成验证**

Run: `git status --short`
Expected: 干净工作树（无未提交改动）。
