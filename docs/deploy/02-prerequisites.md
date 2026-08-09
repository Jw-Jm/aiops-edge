# 02 环境准备与前置

> 部署前需要准备的环境、工具与资源，以及国内网络源配置。
> 覆盖：本机（OrbStack K8s）与生产集群两种场景。

---

## 1. 环境要求

| 项 | 本机开发（OrbStack） | 生产集群 |
|----|--------------------|---------|
| K8s 版本 | OrbStack 内置 K8s（≥1.24） | ≥1.24 |
| Docker | OrbStack Docker | 任一容器运行时 |
| Helm | ≥3.0 | ≥3.0 |
| kubectl | 有 | 有 |
| 架构 | arm64（OrbStack 原生） | amd64（绝大多数生产） |
| 存储类 | 集群默认（`local-path`） | 生产存储类（如 `nfs-client`/云盘） |

> **架构差异**：本机为 arm64，生产多为 amd64。构建镜像时通过 `BUILD_PLATFORM` 指定，见本章 §3。

---

## 2. 工具安装

### 2.1 OrbStack（本机开发，macOS）
- 安装 [OrbStack](https://orbstack.dev)，启用内置 K8s
- 确认：
  ```bash
  kubectl config current-context   # 应为 orbstack
  ```

### 2.2 Helm
```bash
brew install helm          # macOS
# 或 Linux: curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
helm version               # ≥ v3.0
```

### 2.3 Docker
- 本机：OrbStack 自带 Docker
- 生产：任一容器运行时即可（镜像构建可在 CI 完成）

---

## 3. 镜像构建（可移植配置）

> `deploy/scripts/build-images.sh` 支持通过环境变量覆盖 registry / tag / 平台，
> **不写死本环境**。生产跨平台/跨集群拉取时用它。

```bash
# 本机默认（无 registry，本地 tag，当前架构）
./deploy/scripts/build-images.sh

# 指定 registry + 版本化 tag（生产，可复现、可回滚）
IMAGE_REGISTRY=registry.example.com/aiops \
IMAGE_TAG=v1.2.0 \
./deploy/scripts/build-images.sh

# 指定构建平台（如生产 amd64）
BUILD_PLATFORM=linux/amd64 \
./deploy/scripts/build-images.sh

# 只构建单个服务
./deploy/scripts/build-images.sh query-api
```

**构建的 5 个镜像**：

| 服务 | 镜像名 |
|------|--------|
| frontend | `observability-frontend` |
| query-api | `query-api` |
| ingest | `ingest-pipeline` |
| ai-orchestrator | `ai-orchestrator` |
| ipmi-exporter | `ipmi-exporter` |

> 有 registry 时镜像名为 `registry.example.com/aiops/<name>:<tag>`；
> 无 registry 时为 `<name>:latest`（本机 K8s 直接使用本地镜像）。

---

## 4. 国内网络源配置（大陆网络环境）

> 直连 Docker Hub / PyPI / Debian 可能超时。以下配置确保构建与拉取可用。

### 4.1 Docker 镜像拉取（registry 镜像加速）
```bash
# 方案 A：临时指定 registry 前缀（最简）
# 在 build-images.sh 的 IMAGE_REGISTRY 前缀上叠加国内镜像源前缀，例如：
#   docker build 基础镜像改用国内镜像源
docker pull docker.1ms.run/library/nginx:alpine   # 示例：国内源拉 nginx

# 方案 B：配置 Docker daemon registry-mirrors（推荐，一劳永逸）
# macOS: ~/.docker/daemon.json
{
  "registry-mirrors": ["https://docker.1ms.run", "https://docker.m.daocloud.io"]
}
```

### 4.2 pip（Python 依赖，ai-orchestrator）
```bash
pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r requirements.txt
```

### 4.3 apt（基础镜像构建内）
```dockerfile
# 替换 sources.list 为清华/阿里源
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null \
 || sed -i 's|archive.ubuntu.com|mirrors.aliyun.com|g; s|security.ubuntu.com|mirrors.aliyun.com|g' /etc/apt/sources.list
```

### 4.4 npm（前端依赖，observability-frontend）
```bash
npm config set registry https://registry.npmmirror.com
```

### 4.5 Helm 仓库（deepflow）
```bash
helm repo add deepflow https://deepflowio.github.io/deepflow
helm repo update deepflow
```

---

## 5. 前置资源

### 5.1 存储类（PVC）
- 本机：默认 `local-path` 即可
- 生产：在 `values.yaml` / `values-prod.yaml` 指定真实 `storageClass`
- 要求：`ReadWriteOnce` 支持；高可用场景用云盘/NFS 的多可用区存储类

### 5.2 密钥（生产必读）
生产部署前需准备强随机密钥（**≥32 字符**），生成建议：
```bash
openssl rand -hex 32    # jwtSecret / llmEncryptionKey（≥32 字节）
openssl rand -hex 24    # internalToken / ingestApiKey
openssl rand -hex 16    # clickhousePassword / redisPassword / mysqlRootPassword
```

> 完整密钥注入方法见《[04 生产配置与密钥](./04-prod-config.md)》。

---

## 6. 可移植性检查清单

部署到新环境前，确认以下项均可覆盖（无本环境写死）：

| 配置 | 覆盖方式 |
|------|---------|
| 镜像 registry/tag/架构 | `IMAGE_REGISTRY` / `IMAGE_TAG` / `BUILD_PLATFORM`（build 脚本）|
| 镜像引用 | `values.yaml` 各组件 `image` 字段 |
| 存储类 | `values.yaml` / `values-prod.yaml` `storageClass` |
| 中间件地址 | `values.yaml` `*.external.host`（复用外部实例）|
| DeepFlow/Grafana URL | 前端 `VITE_DEEPFLOW_URL` / `VITE_GRAFANA_URL` |
| 租户 ID | 前端 `VITE_TENANT_ID` |
| LLM 配置 | 前端设置页 / `aiOrchestrator` 配置 |
| 节点 IP（node-exporter） | `values.yaml` `nodeExporterTarget` |

---

> 下一章：《[03 部署步骤与验证](./03-deploy.md)》
