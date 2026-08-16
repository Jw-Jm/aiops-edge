# AIOps 平台部署使用手册

> 本手册面向 **部署/运维工程师**，覆盖生产环境部署、密钥注入、配置、使用、运维排障。
> 代码层面：Helm Chart（`deploy/helm/aiops`）+ 一键脚本（`deploy/scripts`）。

---

## 快速导航

| 章节 | 内容 | 适用 |
|------|------|------|
| [01 架构与组件](./01-architecture.md) | 系统架构、服务职责、数据流、中间件清单 | 理解系统 |
| [02 环境准备与前置](./02-prerequisites.md) | K8s/Helm/Docker、镜像构建、国内源、可移植性 | 部署前 |
| [03 部署步骤与验证](./03-deploy.md) | 本机快速部署、生产部署、验证方法 | 部署 |
| [04 生产配置与密钥](./04-prod-config.md) | 密钥注入、values-prod、可移植、高可用(HA)、密钥外部化(KMS/ESO) | 上生产前必读 |
| [05 使用指南](./05-usage.md) | 前端功能模块使用、AI 诊断审批流程 | 使用者 |
| [06 运维与排障](./06-ops.md) | 升级、备份恢复、日志、常见问题排查 | 运维 |
| [07 多集群接入（中心→边缘）](./07-multicluster-edge.md) | 中心平台向被纳管集群下发组件清单、三层数据接入、隔离网络联邦式 | 多集群架构 |

---

## 速览

- **架构**：4 自研服务（frontend/query-api/ingest/ai-orchestrator）+ 8 中间件 + deepflow 采集
- **命名空间**：`observability`（自研+中间件）、`deepflow`（采集栈）
- **主入口**：NodePort `30253`
- **一键本机部署**：`./deploy/scripts/build-images.sh && ./deploy/scripts/apply.sh`
- **生产部署**：镜像构建（可配置 registry/tag/平台）+ `helm upgrade -f values-prod.yaml`

---

## 部署链路速查

```bash
# 1. 构建镜像（可配置 registry/tag/平台）
IMAGE_REGISTRY=reg.example.com/aiops IMAGE_TAG=v1.2.0 BUILD_PLATFORM=linux/amd64 \
  ./deploy/scripts/build-images.sh

# 2. 部署 observability（含自动建表）—— 生产用 values-prod.yaml 注入密钥
helm upgrade --install aiops deploy/helm/aiops \
  --namespace observability --create-namespace \
  --values deploy/helm/aiops/values-prod.yaml --wait --timeout 15m

# 3. 部署 deepflow 采集栈
helm upgrade --install deepflow deepflow/deepflow --version 7.1.002 \
  --namespace deepflow --create-namespace \
  --values deploy/helm/aiops/values-deepflow.yaml --wait --timeout 15m

# 4. 验证
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/   # 期望 200
```

---

## 关键入口

| 资源 | 位置 |
|------|------|
| 主 Chart | `deploy/helm/aiops/` |
| 本机配置 | `deploy/helm/aiops/values.yaml` |
| 生产配置 | `deploy/helm/aiops/values-prod.yaml` |
| deepflow 配置 | `deploy/helm/aiops/values-deepflow.yaml` |
| 一键脚本 | `deploy/scripts/`（build-images / apply / destroy / init-db）|
| SNMP+IPMI 专项 | `deploy/SNMP_IPMI_DEPLOYMENT.md` |
