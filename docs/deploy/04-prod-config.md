# 04 生产配置与密钥

> 面向生产部署的配置指南：密钥注入、values-prod 覆盖、可移植性、高可用（HA）。
> 这是**上生产前必须通读**的一章。

---

## 1. 密钥管理（强制）

### 1.1 必须注入的密钥

| 密钥 | 用途 | 要求 |
|------|------|------|
| `jwtSecret` | 登录 JWT 签名 | ≥32 字符，缺失/过短则 query-api 拒绝启动 |
| `llmEncryptionKey` | LLM API key 加密（AES-256） | ≥32 字节，缺失则拒绝启动 |
| `internalToken` | 服务间调用令牌 | 强随机 |
| `ingestApiKey` | 采集端 X-Api-Key 鉴权 | 强随机 |
| `clickhousePassword` | ClickHouse 认证 | 强随机 |
| `redisPassword` | Redis requirepass | 强随机 |
| `mysqlRootPassword` | MySQL root | 强随机 |
| `minioAccessKey` / `minioSecretKey` | MinIO 凭证 | ≥3 / ≥8 字符，禁弱口令 |

### 1.2 生成方式
```bash
openssl rand -hex 32    # jwtSecret / llmEncryptionKey
openssl rand -hex 24    # internalToken / ingestApiKey
openssl rand -hex 16    # clickhousePassword / redisPassword / mysqlRootPassword
openssl rand -hex 24    # minioSecretKey
```

### 1.3 注入方式（三选一）

**方式 A：values-prod.yaml（推荐）**
```yaml
secrets:
  jwtSecret: "xxxxxx32chars..."        # 替换 CHANGE_ME
  llmEncryptionKey: "xxxxxx32chars..."
  internalToken: "..."
  ingestApiKey: "..."
  clickhousePassword: "..."
  redisPassword: "..."
  minioAccessKey: "..."
  minioSecretKey: "..."
  mysqlRootPassword: "..."
```

**方式 B：helm --set**
```bash
helm upgrade --install aiops ... \
  --set secrets.jwtSecret="..." --set secrets.llmEncryptionKey="..." ...
```

**方式 C：外部 Secret（推荐生产，密钥不进 values 文件）**
- 先用 `kubectl create secret generic aiops-secrets --from-literal=...` 创建
- 或接入 `external-secrets` / vault
- 注意：Chart 的 `secrets.yaml` 模板会覆盖同名 Secret，外部 Secret 需适配（或改模板）

> **安全兜底**：密钥缺失/过短/为 `CHANGE_ME` 时，Helm `required` 报错 + query-api 启动
> panic。**这是故意设计**，防止弱密钥/占位符上生产。

---

## 2. values-prod.yaml 生产覆盖

### 2.1 完整示例
```yaml
# 无状态服务副本数（多副本 HA）
replicaCount: 3
# 有状态单写组件副本数（勿改，单写安全）
statefulReplicaCount: 1

# 存储类
clickhouse:
  storageClass: "standard"        # 生产存储类
victoriaMetrics:  { storageClass: "standard" }
victoriaLogs:     { storageClass: "standard" }
redis:            { storageClass: "standard" }
minio:            { storageClass: "standard" }
mysql:            { storageClass: "standard" }

# 中间件地址（若复用外部实例，设 enabled:false + external.host）
clickhouse:
  enabled: true
  external: { host: "ch.example.com", port: "8123" }

# LLM 接真实模型（关闭 mock）
aiOrchestrator:
  llmMock: "false"

# 镜像（registry + 版本 tag）
frontend:     { image: "registry.example.com/aiops/frontend:v1.2.0" }
queryApi:     { image: "registry.example.com/aiops/query-api:v1.2.0" }
ingest:       { image: "registry.example.com/aiops/ingest-pipeline:v1.2.0" }
aiOrchestrator: { image: "registry.example.com/aiops/ai-orchestrator:v1.2.0" }

# 密钥
secrets:
  jwtSecret: "..."
  # ...（见上文）
```

### 2.2 覆盖逻辑
`helm upgrade --install aiops . -f values-prod.yaml`：`values-prod.yaml` 会**合并覆盖** `values.yaml`
默认值，未覆盖项用默认值。

---

## 3. 可移植性配置

### 3.1 镜像
- build 脚本：`IMAGE_REGISTRY` / `IMAGE_TAG` / `BUILD_PLATFORM`（见 02 章 §3）
- Chart：各组件 `image` 字段指向带 registry 的完整镜像名

### 3.2 存储类
- `storageClass` 全组件可配置（PVC 用 `storageClassName`）
- 生产指定真实 SC，避免用 `local-path`（节点本地盘，无数据冗余）

### 3.3 中间件复用外部实例
```yaml
# 已有生产 ClickHouse/MySQL/Redis/MinIO 时，不重复部署，直接复用：
clickhouse:
  enabled: false
  external: { host: "ch.prod.internal", port: "8123" }
mysql:
  enabled: false
  external: { host: "mysql.prod.internal", port: "3306" }
# 其余同理（victoriaMetrics / victoriaLogs / redis / minio）
```

### 3.4 前端环境变量（构建时注入）
前端通过 `VITE_*` 环境变量配置，避免硬编码：
```bash
# 构建前端镜像时注入（在 observability-frontend 目录）
VITE_TENANT_ID=prod \
VITE_DEEPFLOW_URL=http://deepflow-server.deepflow.svc.cluster.local:30417 \
VITE_GRAFANA_URL=http://deepflow-grafana.deepflow.svc.cluster.local:32060 \
npm run build
```
| VITE_ 变量 | 用途 |
|-----------|------|
| `VITE_TENANT_ID` | 租户 ID（默认 `default`）|
| `VITE_DEEPFLOW_URL` | DeepFlow Server 地址 |
| `VITE_GRAFANA_URL` | DeepFlow Grafana 地址 |

---

## 4. LLM 真实接入

### 4.1 关闭 mock 并配置
```yaml
aiOrchestrator:
  llmMock: "false"   # 关键：生产必须关闭 mock，否则 AI 诊断返回假文本
```
在**前端「系统设置 → LLM」**配置真实 API Key / Base URL / 模型。API Key 经 query-api
用 `llmEncryptionKey` **AES 加密存储**（MySQL），不回显明文。

### 4.2 支持的模型
OpenAI 兼容接口即可（OpenAI / DeepSeek / Qwen / 本地 vLLM 等）。配置 `provider` 的
`base_url` 与 `default_model`。

---

## 5. 高可用（HA）

> 单写组件 `replicas:1` 是**单写安全兜底**（避免共享 RWO PVC 损坏）。真正的 HA 需要
> **数据冗余 + 自动切换**。按业务影响排序：

### 5.1 MySQL（P0，唯一事务库）
| 方案 | 说明 |
|------|------|
| **Percona XtraDB Cluster (PXC) Operator** | 多副本 + 自动选主 + 自动 failover，推荐 |
| 主从复制 + MHA/Orchestrator | 1 主 + 1 从，半同步，自动提升 |
| 云托管 MySQL（RDS 等） | 完全托管，最简单 |

**落地**：MySQL 的 HA 用 Operator 或外部托管，Chart 的 `mysql.enabled:false` + `external.host` 复用。

### 5.2 MinIO（P0，全组件数据底座）
MinIO 原生支持 **Erasure Coding 分布式**：
- ≥4 节点 4 盘，容 1 节点/盘故障
- 把单点 Deployment 改为分布式 StatefulSet，每节点独立 PVC
- 或直接用托管对象存储（S3/OSS）

### 5.3 ClickHouse（P1，量大，查询可扩展）
- 表引擎改 `ReplicatedMergeTree`
- 多副本（每副本独立 PVC，`volumeClaimTemplates` 天然支持）
- 配 ClickHouse Keeper（替代 ZooKeeper）+ cluster
- query-api/ingest 指向多副本（headless service）

### 5.4 Redis（P1，任务队列）
- **Redis Sentinel**：1 主 + 2 从 + 3 Sentinel，自动故障转移
- 或云托管 Redis
- ai-orchestrator 的 ARQ 需要 Redis 高可用

### 5.5 VictoriaMetrics / VictoriaLogs（P2）
- 单节点写**对象存储**（`-remoteWrite.storageDataPath` 或 vmbackup 到 MinIO/S3）
- 或 VMCluster（vmstorage 多副本）
- 定期备份到对象存储

### 5.6 通用手段（所有组件）
- **PVC 快照**：云盘/存储类快照，定期对 RWO PVC 快照
- **定时备份**：MySQL dump、CH `clickhouse-backup`、VM `vmbackup`
- **多可用区**：节点分布多个 AZ（需多 AZ 存储类）
- **PodDisruptionBudget (PDB)**：多副本服务保证最少可用（单副本意义有限）

### 5.7 无状态服务 HA（query-api / frontend）— 偏差 B10 落地

> 有状态单写组件（ingest / ai-orchestrator / CH / MySQL 等）因 RWO PVC 约束维持单副本，
> **HA 重点放在无状态服务**：query-api（多副本 + PDB + HPA）与 frontend（多副本 + PDB）。

**1) 多副本**：`values-prod.yaml` 已设 `replicaCount: 2`，覆盖 query-api / frontend。
```yaml
replicaCount: 2
```
> 注意：ingest / ai-orchestrator 因挂 RWO PVC 固定 1 副本（多副本需先外部化存储，见 §5.3/§5.4）。

**2) PodDisruptionBudget（PDB）**：新增 `templates/query-api/pdb.yaml` 与 `templates/frontend/pdb.yaml`：
```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: query-api
  namespace: {{ .Values.namespace.observability }}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: query-api
```
（frontend 同款，`app: frontend`。有状态组件在单副本下 PDB 无意义，不配置。）

**3) HorizontalPodAutoscaler（HPA）**：新增 `templates/query-api/hpa.yaml`（可选，frontend 同款）：
```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: query-api
  namespace: {{ .Values.namespace.observability }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: query-api
  minReplicas: {{ .Values.hpa.queryApi.min | default 2 }}
  maxReplicas: {{ .Values.hpa.queryApi.max | default 6 }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```
**4) 验证**：`helm template` 渲染出 PDB/HPA 资源；`kubectl get pdb,hpa -n observability` 确认生效。

---

### 5.8 密钥外部化（External Secrets / KMS）— 偏差 B11 落地

> 现状：`secrets.yaml` 用 Helm `required` 守卫强校验，密钥明文存在于 `aiops-secrets` Secret。
> 生产建议接入外部密钥管理，避免密钥进 Git / values 文件。

**方案 A：External Secrets Operator（ESO，推荐）**
1. 部署 ESO + provider（AWS Secrets Manager / Vault / GCP Secret Manager 等）
2. 新增 `templates/external-secret.yaml`（示例 AWS）：
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: aiops-secrets
  namespace: {{ .Values.namespace.observability }}
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aiops-store
    kind: SecretStore
  target:
    name: aiops-secrets
  data:
    - secretKey: JWT_SECRET
      remoteRef: { key: aiops-prod, property: jwtSecret }
    - secretKey: LLM_ENCRYPTION_KEY
      remoteRef: { key: aiops-prod, property: llmEncryptionKey }
    # ... INTERNAL_TOKEN / INGEST_API_KEY / CLICKHOUSE_PASSWORD / MYSQL_ROOT_PASSWORD 同理
```
3. 将 Chart 的 `secrets.yaml` 模板改为 `enabled: false`（或加 `externalSecrets.enabled` 开关跳过渲染），由 ESO 管理同名 Secret。
4. 密钥轮换：ESO 按 `refreshInterval` 自动同步；轮换后需滚动重启 query-api / ingest / orchestrator 加载新值。

**方案 B：HashiCorp Vault**：ESO 的 SecretStore 指向 Vault（`auth` 用 Kubernetes 服务账号），密钥统一从 Vault 读取，审计/轮换/撤销由 Vault 管理。

**安全基线补充**（追加到 §6 清单）：
- [ ] 无状态服务已配 PDB；生产规模下配置 HPA
- [ ] 密钥已外部化（ESO/Vault），不落 Git / values 文件
- [ ] 密钥轮换流程已演练（改 KMS → ESO 同步 → 滚动重启验证）

---

## 6. 生产安全基线清单

- [ ] 所有密钥已注入强随机值（非 `CHANGE_ME`/dev 弱值）
- [ ] ClickHouse/MySQL/Redis/MinIO 均启用认证（本次已默认开启）
- [ ] LLM mock 已关闭，API Key 加密存储
- [ ] 镜像用版本化 tag + 私有 registry（可复现、可回滚）
- [ ] 存储类为生产 SC（非 local-path 单节点盘）
- [ ] DeepFlow/Grafana 地址用 `VITE_*` 配置（非硬编码）
- [ ] WebShell/命令执行白名单仅放行运维所需命令
- [ ] 关闭不需要的采集器（如无 IPMI 则 `ipmiExporter.enabled:false`）

---

> 下一章：《[05 使用指南](./05-usage.md)》
