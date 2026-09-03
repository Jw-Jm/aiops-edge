# Python 依赖（P1-SUP1）：从多平台 wheelhouse + 带 hash 的 requirements-lock.txt 确定性构建。
# 不再采用"从已运行容器导出 site-packages"（原 bin/sp.tar.gz）的权威构建方式。
# deps 阶段只做校验安装，wheelhouse（~1GB）只存在于该中间层，不进入最终镜像/registry。
ARG TARGETARCH
FROM docker.m.daocloud.io/library/python:3.12-slim AS deps
ARG TARGETARCH
WORKDIR /wheels
COPY wheelhouse.sha256 /wheels/wheelhouse.sha256
COPY wheelhouse/ /wheels/wheelhouse/
COPY requirements-lock.txt /wheels/requirements-lock.txt
RUN echo ">>> [sup1] 校验 wheelhouse 完整性 (sha256sum -c)" && \
    (cd wheelhouse && sha256sum -c ../wheelhouse.sha256) && \
    echo ">>> [sup1] 离线安装锁定依赖 (--no-index --require-hashes, arch=${TARGETARCH})" && \
    python -m pip install --no-index --find-links=wheelhouse/${TARGETARCH} \
        --require-hashes -r requirements-lock.txt && \
    rm -rf /wheels

FROM docker.m.daocloud.io/library/python:3.12-slim AS runtime
ARG TARGETARCH
WORKDIR /app

# System deps + k8sgpt binary
# procps: ps/top/free | iproute2: ss | net-tools: netstat | iputils-ping: ping
# 用于 RCA 假设引擎的诊断检查命令，补齐容器内缺失的系统工具
# TARGETARCH is auto-injected by docker build, matching the build platform (arm64/amd64).
# Docker convention: amd64 -> "amd64", arm64 -> "arm64" (no "aarch64" variant needed).
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
# 从 deps 阶段拷贝校验安装好的依赖（site-packages + console scripts），零网络。
COPY --from=deps /usr/local/lib/python3.12/site-packages /usr/local/lib/python3.12/site-packages
COPY --from=deps /usr/local/bin/uvicorn /usr/local/bin/uvicorn
COPY --from=deps /usr/local/bin/chroma /usr/local/bin/chroma
RUN chmod +x /usr/local/bin/uvicorn /usr/local/bin/chroma 2>/dev/null || true

# 嵌入模型缓存（完全离线）：从本地 bin/hf.tar.gz 直接解压 HF 模型缓存（bge-small-zh）。
# ChromaDB onnx 模型（all-MiniLM-L6-v2, ~79MB）：从本地 bin/chroma.tar.gz 解压。
# P1-SUP1: models.sha256 入 Git，解压前校验 sha256 一致。
COPY models.sha256 /tmp/models.sha256
COPY bin/hf.tar.gz /tmp/hf.tar.gz
COPY bin/chroma.tar.gz /tmp/chroma.tar.gz
RUN echo ">>> [4/6] 校验并解压 HF + ChromaDB 模型缓存 (离线包)" && \
    (cd /tmp && sha256sum -c models.sha256) && \
    mkdir -p /root/.cache && \
    tar xzf /tmp/hf.tar.gz -C /root/.cache/ && \
    tar xzf /tmp/chroma.tar.gz -C /root/.cache/ && \
    rm -f /tmp/hf.tar.gz /tmp/chroma.tar.gz /tmp/models.sha256

# 二进制工具：k8sgpt + kubectl 均用本地已下载好的二进制 (COPY 进镜像)，完全离线。
# P1-SUP1: tools.sha256 入 Git，构建时校验其 sha256 与 tools.sha256 一致。
COPY tools.sha256 /tmp/tools.sha256
COPY bin/k8sgpt /usr/local/bin/k8sgpt
COPY bin/kubectl /usr/local/bin/kubectl
RUN echo ">>> [5/6] 校验并赋权二进制工具 (k8sgpt + kubectl)" && \
    (cd /usr/local/bin && sha256sum -c /tmp/tools.sha256) && \
    chmod +x /usr/local/bin/k8sgpt /usr/local/bin/kubectl && \
    rm -f /tmp/tools.sha256
# 运行时代码（精确 COPY 白名单，不进 bin/wheelhouse；wheelhouse 仅 deps 阶段存在）
COPY *.py ./
COPY acl/ acl/
COPY apps/ apps/
COPY data/ data/
COPY flow_engine/ flow_engine/
COPY kg/ kg/
COPY migrations/ migrations/
COPY personas/ personas/
COPY rca_engine/ rca_engine/
COPY scripts/ scripts/
COPY skills/ skills/
COPY tools.sha256 models.sha256 ./
EXPOSE 8080
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080", "--timeout-keep-alive", "120", "--limit-concurrency", "20"]
