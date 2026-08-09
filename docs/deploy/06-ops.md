# 06 运维与排障

> 部署后的日常运维：升级、清理、日志查看、备份恢复、常见问题排查。
> 面向部署/运维工程师。

---

## 1. 日常运维命令

### 1.1 查看组件状态
```bash
kubectl -n observability get pods,svc,pvc
kubectl -n deepflow get pods
kubectl -n observability get events --sort-by=.lastTimestamp | tail -20   # 近期事件
```

### 1.2 查看日志
```bash
# 单个服务
kubectl -n observability logs deploy/query-api
kubectl -n observability logs deploy/ai-orchestrator
kubectl -n observability logs deploy/ingest
kubectl -n observability logs deploy/frontend

# 实时跟随
kubectl -n observability logs -f deploy/query-api

# 崩溃重启的容器
kubectl -n observability logs deploy/query-api --previous
```

### 1.3 进入容器排障
```bash
kubectl -n observability exec -it deploy/query-api -- sh
kubectl -n observability exec -it clickhouse-0 -- clickhouse-client --password "$CH_PASSWORD"
```

---

## 2. 升级

### 2.1 应用更新（镜像升级）
```bash
# 1) 构建新镜像（或 CI 推送）
IMAGE_REGISTRY=registry.example.com/aiops IMAGE_TAG=v1.3.0 ./deploy/scripts/build-images.sh

# 2) helm upgrade（自动滚动更新）
helm upgrade --install aiops deploy/helm/aiops \
  --namespace observability \
  --values deploy/helm/aiops/values-prod.yaml \
  --set frontend.image="registry.example.com/aiops/frontend:v1.3.0" \
  --set queryApi.image="registry.example.com/aiops/query-api:v1.3.0" \
  # ... 其余服务同理
  --wait --timeout 15m
```

### 2.2 回滚
```bash
helm rollback aiops <revision> --namespace observability
# 查看历史版本
helm history aiops --namespace observability
```

### 2.3 数据库 schema 升级
- ClickHouse 建表用幂等 `IF NOT EXISTS`，新列/新表随 helm 自动执行 init Job
- 若新增列需迁移，用 `deploy/scripts/init-db.sh` 手动执行迁移 SQL

---

## 3. 备份与恢复

> 单写组件数据都在 PVC。**生产必须做定期备份**（尤其 MySQL/ClickHouse）。

### 3.1 MySQL 备份
```bash
# 备份
kubectl -n observability exec -it mysql-0 -- sh -c \
  'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" aiops > /tmp/aiops.sql'
kubectl -n observability cp mysql-0:/tmp/aiops.sql ./backup-$(date +%F).sql

# 恢复
kubectl -n observability cp ./backup.sql mysql-0:/tmp/backup.sql
kubectl -n observability exec -it mysql-0 -- sh -c \
  'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" aiops < /tmp/backup.sql'
```

### 3.2 ClickHouse 备份
```bash
# 用 clickhouse-backup（生产部署于 Job/CronJob）
kubectl -n observability exec -it clickhouse-0 -- \
  clickhouse-backup create backup_$(date +%F) --password "$CH_PASSWORD"
kubectl -n observability exec -it clickhouse-0 -- \
  clickhouse-backup restore backup_2026-01-01
```

### 3.3 PVC 快照（推荐）
```bash
# 用存储类快照（需 SC 支持 VolumeSnapshot）
kubectl apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: mysql-snap-$(date +%F)
  namespace: observability
spec:
  volumeSnapshotClassName: <sc-snapshot-class>
  source: { persistentVolumeClaimName: data-mysql-0 }
EOF
```

---

## 4. 常见问题排查（Troubleshooting）

### 4.1 Pod 启动失败 / CrashLoopBackOff
```bash
kubectl -n observability describe pod <pod>     # 看事件
kubectl -n observability logs <pod> --previous  # 看崩溃前日志
```
**常见原因**：
| 现象 | 原因 | 处理 |
|------|------|------|
| query-api panic "JWT_SECRET" | 密钥未注入/<32 字符 | 注入 `secrets.jwtSecret` 强密钥 |
| query-api panic "LLM_ENCRYPTION_KEY" | 加密密钥未注入 | 注入 `secrets.llmEncryptionKey` |
| ImagePullBackOff | 镜像不可见（无 registry/未推） | 构建并推送镜像到集群可访问 registry |
| CrashLoop（ClickHouse init 失败） | CH 密码与 init Job 不一致 | 确保 `CLICKHOUSE_PASSWORD` 全组件一致 |

### 4.2 ClickHouse 连接失败
```bash
# 用密码验证
kubectl -n observability exec clickhouse-0 -- clickhouse-client \
  --password "$CH_PASSWORD" --query "SELECT 1"
```
**常见原因**：query-api/ingest 的 `CLICKHOUSE_USER/PASSWORD` 与 ClickHouse 实际不一致。
> 注意：ClickHouse 现已启用密码认证（init 容器从 Secret 生成 `password_sha256_hex`），
> 空密码连接会失败——这是安全加固，非故障。

### 4.3 Redis 连接失败
- Redis 启用 `requirepass`，ai-orchestrator 需 `REDIS_PASSWORD`（Secret 注入）
- `redis-cli -a "$REDIS_PASSWORD" ping` 验证

### 4.4 前端打不开 / 白屏
```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:30253/   # 期望 200
kubectl -n observability logs deploy/frontend
```
- NodePort 未暴露：确认 frontend Service `nodePort: 30253`
- 代理 502：确认 query-api / ai-orchestrator 正常

### 4.5 AI 诊断无响应
- 确认 `LLM_MOCK`：生产应为 `false`（mock 返回假文本）
- 确认 LLM 配置（前端设置页）API Key/Base URL 正确
- 确认 orchestrator 能访问 query-api（`QUERY_API_URL`）

### 4.6 数据不落 ClickHouse
```bash
# 确认表存在
kubectl -n observability exec clickhouse-0 -- clickhouse-client --password "$CH_PASSWORD" \
  --query "SELECT name FROM system.tables WHERE database='observability'"
# 确认 ingest 日志
kubectl -n observability logs deploy/ingest | tail -50
```
- 表不存在：`./deploy/scripts/init-db.sh`
- ingest 报 CH 认证失败：检查 `CLICKHOUSE_USER/PASSWORD`

### 4.7 存储/磁盘占用
- `trace_spans` / `log_records` / `service_topology` 已设 **TTL 30 天**，自动清理
- `alert_events` TTL 30 天
- 监控 PVC 使用率：`kubectl -n observability get pvc`

---

## 5. 安全运维

- **密钥轮换**：定期更换 `JWT_SECRET`/`llmEncryptionKey` 等（需重启 query-api）
- **审计日志**：`/audit` 页查看全部操作审计（写库，不可篡改）
- **最小权限**：只给需要的用户 `admin` 角色；WebShell/执行白名单最小化
- **镜像扫描**：CI 中对自研镜像做漏洞扫描
- **RBAC**：query-api 用最小化 ServiceAccount（只读 view + node-reader）

---

## 6. 卸载与清理

```bash
# 卸载（保留 PVC）
./deploy/scripts/destroy.sh

# 彻底清除（含 PVC/namespace 数据）
./deploy/scripts/destroy.sh --purge-data

# 手动清理残留
kubectl delete ns observability deepflow --wait
```

> 备份数据在删除 PVC 前先下载（见 §3）。

---

## 7. 监控与告警自检

- 平台自身指标：query-api `/metrics`、ingest `/metrics`（走 VictoriaMetrics 抓取）
- 建议为**平台自身**配置监控（CPU/内存/磁盘），避免"监控自身宕机"
- 关键组件的持久化 PVC 建议配置磁盘使用率告警

---

> 返回《[部署手册索引](./README.md)》
