# 03 部署步骤与验证

> 本文档说明 AIOps 平台的完整部署流程与部署后验证方法。
> 场景一：本机快速部署（OrbStack）；场景二：生产环境部署（可移植）。
> 底层用 Helm Chart（`deploy/helm/aiops`）+ 一键脚本（`deploy/scripts`）。

---

## 1. 部署架构

```
deploy/
├── helm/aiops/           # 主 Chart（4 自研服务 + 8 中间件 + 自动建表）
│   ├── values.yaml       # 本机默认配置
│   ├── values-prod.yaml  # 生产 overrides（密钥/存储类/副本）
│   └── values-deepflow.yaml # deepflow 子 chart 配置
└── scripts/
    ├── build-images.sh   # 构建 5 个自研镜像
    ├── apply.sh          # 一键部署 observability + deepflow
    ├── destroy.sh        # 卸载（默认保留 PVC）
    └── init-db.sh        # 手动建表逃生门
```

---

## 2. 场景一：本机快速部署（OrbStack K8s）

### 2.1 前置确认
```bash
kubectl config current-context   # = orbstack
helm version                     # ≥ v3
docker info >/dev/null           # Docker 可用
```

### 2.2 构建镜像
```bash
cd aiops
./deploy/scripts/build-images.sh   # 构建 5 个自研服务镜像（arm64 本地）
```

### 2.3 一键部署
```bash
./deploy/scripts/apply.sh
```

`apply.sh` 会依次：
1. 添加/更新 `deepflow` Helm 仓库
2. `helm upgrade --install aiops` 部署 observability（自研 + 中间件，**自动建表**）
3. `helm upgrade --install deepflow` 部署 deepflow 到独立 `deepflow` namespace
4. `--wait` 等待就绪

> **注意**：`apply.sh` 注入的是**开发用默认密钥**。生产环境**不要**用 apply.sh，改用场景二。

### 2.4 访问
- 浏览器访问 `http://localhost:30253/`
- 默认管理员账号见《05 使用指南》登录章节

---

## 3. 场景二：生产环境部署（可移植）

### 3.1 构建并推送镜像
```bash
# 指定 registry + 版本 tag + amd64 平台
IMAGE_REGISTRY=registry.example.com/aiops \
IMAGE_TAG=v1.2.0 \
BUILD_PLATFORM=linux/amd64 \
./deploy/scripts/build-images.sh

# 推送镜像到 registry（脚本不自动 push，需手动）
docker push registry.example.com/aiops/query-api:v1.2.0
# ... 其余 4 个镜像同理
```

### 3.2 准备生产配置
编辑 `deploy/helm/aiops/values-prod.yaml`（或用 `--set` 覆盖）：
- **密钥**：填入强随机值（见 02 章 §5.2 生成方式）
- **存储类**：指定生产 `storageClass`
- **副本数**：无状态服务 `replicaCount`；有状态单写组件固定 `statefulReplicaCount: 1`
- **镜像**：覆盖各组件 `image` 为 `registry.example.com/aiops/<name>:<tag>`

### 3.3 部署
```bash
cd aiops
helm repo add deepflow https://deepflowio.github.io/deepflow
helm repo update deepflow

# 1) observability（生产配置 + 自动建表）
helm upgrade --install aiops deploy/helm/aiops \
  --namespace observability --create-namespace \
  --values deploy/helm/aiops/values-prod.yaml \
  --wait --timeout 15m

# 2) deepflow
helm upgrade --install deepflow deepflow/deepflow \
  --version 7.1.002 \
  --namespace deepflow --create-namespace \
  --values deploy/helm/aiops/values-deepflow.yaml \
  --wait --timeout 15m
```

> **密钥强校验**：`values-prod.yaml` 的 `jwtSecret`/`llmEncryptionKey` 若为 `CHANGE_ME`
> 或 <32 字符，Helm 渲染会 `required` 报错，query-api 也会启动 panic——这是**安全兜底**，
> 防止弱密钥上生产。

---

## 4. 部署后验证

### 4.1 Pod 状态
```bash
kubectl -n observability get pods      # 全部 Running/Completed
kubectl -n deepflow get pods           # deepflow 全部 Running
```

### 4.2 前端可访问
```bash
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/   # 期望 200
```

### 4.3 数据库表已建（自动建表）
```bash
# 本机（无密码）：
kubectl -n observability exec clickhouse-0 -- clickhouse-client \
  --query "SELECT count() FROM system.tables WHERE database='observability'"
# 生产（有密码）：加 --password "$CH_PASSWORD"
# 期望 8 张表：trace_spans / log_records / service_topology / alert_events 等
```

### 4.4 各服务健康
```bash
kubectl -n observability exec deploy/query-api -- wget -qO- http://localhost:8080/health 2>/dev/null || true
kubectl -n observability exec deploy/ai-orchestrator -- wget -qO- http://localhost:8080/health 2>/dev/null || true
```

### 4.5 采集链路（可选）
```bash
# 向 ingest 发一条测试 trace（需 INGEST_API_KEY）
curl -X POST http://<ingest>:8080/v1/traces \
  -H "X-Api-Key: $INGEST_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans":[]}'
```

---

## 5. 手动建表逃生门

正常由 Helm 自动建表。若自动建表失败（Job 未执行/时序问题），用：
```bash
./deploy/scripts/init-db.sh   # 默认 observability namespace
# 指定 namespace: ./deploy/scripts/init-db.sh <namespace>
```

---

## 6. 卸载 / 清理

```bash
# 卸载 release（保留 PVC 数据）
./deploy/scripts/destroy.sh

# 彻底清除（含 PVC / namespace）
./deploy/scripts/destroy.sh --purge-data
```

> 详见《[06 运维与排障](./06-ops.md)》。

---

> 下一章：《[04 生产配置与密钥](./04-prod-config.md)》
