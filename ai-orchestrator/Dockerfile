FROM python:3.12-slim
WORKDIR /app

# System deps + k8sgpt binary
# procps: ps/top/free | iproute2: ss | net-tools: netstat | iputils-ping: ping
# 用于 RCA 假设引擎的诊断检查命令，补齐容器内缺失的系统工具
# TARGETARCH is auto-injected by docker build, matching the build platform (arm64/amd64).
# Docker convention: amd64 -> "amd64", arm64 -> "arm64" (no "aarch64" variant needed).
ARG TARGETARCH
# apt 切国内源 (清华 debian 源)，加速系统包安装
# 可靠覆盖：直接重写 debian.sources (DEB822) 与 sources.list (one-per-line) 两种格式，
# 而非依赖 sed 正则匹配(匹配失败会静默回退官方源)。TARGETARCH 由 docker build 注入。
ARG APT_MIRROR=mirrors.tuna.tsinghua.edu.cn
RUN set -eux; \
    DEBIAN_SRC=/etc/apt/sources.list.d/debian.sources; \
    if [ -f "$DEBIAN_SRC" ]; then \
      sed -i "s@deb.debian.org@${APT_MIRROR}@g; s@security.debian.org@${APT_MIRROR}@g" "$DEBIAN_SRC"; \
    fi; \
    if [ -f /etc/apt/sources.list ]; then \
      sed -i "s@deb.debian.org@${APT_MIRROR}@g; s@security.debian.org@${APT_MIRROR}@g" /etc/apt/sources.list; \
    fi; \
    # 兜底：若 sed 未命中任何 deb.debian.org，直接以清华源重写 debian.sources
    if [ -f "$DEBIAN_SRC" ] && ! grep -q "${APT_MIRROR}" "$DEBIAN_SRC"; then \
      cp "$DEBIAN_SRC" "$DEBIAN_SRC.bak"; \
      { echo "Types: deb"; echo "URIs: http://${APT_MIRROR}/debian/"; echo "Suites: bookworm bookworm-updates bookworm-backports"; echo "Components: main contrib non-free non-free-firmware"; echo "Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg"; } > "$DEBIAN_SRC"; \
    fi
RUN echo ">>> [1/6] 安装系统工具 (apt 国内源)" && \
    apt-get update && apt-get install -y curl git procps iproute2 net-tools iputils-ping \
    sysstat gawk grep sed \
    && apt-get clean && rm -rf /var/lib/apt/lists/*
# Python 依赖（完全离线）：从本地 bin/sp.tar.gz 直接解压已装好的 site-packages，零网络下载。
# 来源：从已运行成功的 ai-orchestrator 容器内打包导出（torch 2.13.0+cpu / chromadb 1.1.1 / sentence-transformers 5.7.0 等全部依赖）
COPY bin/sp.tar.gz /tmp/sp.tar.gz
COPY bin/pybin.tar.gz /tmp/pybin.tar.gz
RUN echo ">>> [3/6] 解压 Python 依赖 (site-packages + console scripts 离线包)" && \
    mkdir -p /usr/local/lib/python3.12 && \
    tar xzf /tmp/sp.tar.gz -C /usr/local/lib/python3.12/ && \
    tar xzf /tmp/pybin.tar.gz -C /usr/local/bin/ && \
    chmod +x /usr/local/bin/uvicorn /usr/local/bin/chroma 2>/dev/null; \
    rm -f /tmp/sp.tar.gz /tmp/pybin.tar.gz
# sp.tar.gz 已包含 apscheduler 3.11.3 + tzlocal 5.4.4（Task 8/9 引入用于 APScheduler 周期巡检）

# 嵌入模型缓存（完全离线）：从本地 bin/hf.tar.gz 直接解压 HF 模型缓存（bge-small-zh）
COPY bin/hf.tar.gz /tmp/hf.tar.gz
# ChromaDB onnx 模型（all-MiniLM-L6-v2, ~79MB）：从本地 bin/chroma.tar.gz 解压，避免运行时从国外源下载
COPY bin/chroma.tar.gz /tmp/chroma.tar.gz
RUN echo ">>> [4/6] 解压 HF + ChromaDB 模型缓存 (离线包)" && \
    mkdir -p /root/.cache && \
    tar xzf /tmp/hf.tar.gz -C /root/.cache/ && \
    tar xzf /tmp/chroma.tar.gz -C /root/.cache/ && \
    rm -f /tmp/hf.tar.gz /tmp/chroma.tar.gz

# 二进制工具：k8sgpt + kubectl 均用本地已下载好的 linux/arm64 版 (COPY 进镜像)，完全离线
COPY bin/k8sgpt /usr/local/bin/k8sgpt
COPY bin/kubectl /usr/local/bin/kubectl
RUN echo ">>> [5/6] 赋权二进制工具 (k8sgpt + kubectl 本地拷贝)" && \
    chmod +x /usr/local/bin/k8sgpt /usr/local/bin/kubectl
COPY . .
# 内置知识库随镜像分发（供启动自动加载 + 上线后重载），避免运行时从外部拉取
COPY data/knowledge_cases.json /app/data/knowledge_cases.json
# 清理构建期的离线依赖包（已被解压到镜像），避免 `COPY . .` 把 bin/ 重复带入镜像导致体积膨胀
RUN rm -rf /app/bin /tmp/*.tar.gz
EXPOSE 8080
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080", "--timeout-keep-alive", "120", "--limit-concurrency", "20"]
