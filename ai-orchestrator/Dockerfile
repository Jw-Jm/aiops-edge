FROM python:3.12-slim
WORKDIR /app

# System deps + k8sgpt binary
# procps: ps/top/free | iproute2: ss | net-tools: netstat | iputils-ping: ping
# 用于 RCA 假设引擎的诊断检查命令，补齐容器内缺失的系统工具
# TARGETARCH is auto-injected by docker build, matching the build platform (arm64/amd64).
# Docker convention: amd64 -> "amd64", arm64 -> "arm64" (no "aarch64" variant needed).
ARG TARGETARCH
# apt 切国内源 (清华 debian 源)，加速系统包安装
RUN sed -i 's@deb.debian.org@mirrors.tuna.tsinghua.edu.cn@g; s@security.debian.org@mirrors.tuna.tsinghua.edu.cn@g' /etc/apt/sources.list.d/debian.sources 2>/dev/null || \
    sed -i 's@deb.debian.org@mirrors.tuna.tsinghua.edu.cn@g; s@security.debian.org@mirrors.tuna.tsinghua.edu.cn@g' /etc/apt/sources.list 2>/dev/null || true
RUN apt-get update && apt-get install -y curl git procps iproute2 net-tools iputils-ping \
    sysstat gawk grep sed \
    && \
    (curl -sL "https://github.com/k8sgpt-ai/k8sgpt/releases/download/v0.4.0/k8sgpt_linux_${TARGETARCH}.tar.gz" | tar xz -C /usr/local/bin k8sgpt) || \
    echo "k8sgpt binary skipped" && \
    # 安装 kubectl（K8s 集群操作，供诊断/执行使用）
    (curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/v1.28.0/bin/linux/${TARGETARCH}/kubectl" && chmod +x /usr/local/bin/kubectl) || \
    echo "kubectl binary skipped" && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Python deps (多国内 pip 源依次尝试: 清华→阿里云→腾讯云, 避免回退到慢的 pypi.org)
# 先安装 CPU 版 torch, 避免 sentence-transformers 自动拉取 GPU/CUDA 依赖(数GB, CPU 服务器用不到)
COPY requirements.txt .
# 优先装 CPU 版 torch(避免拉取数 GB CUDA 依赖); 失败则回退默认源
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu || \
    pip install --no-cache-dir torch || \
    pip install --no-cache-dir -i https://pypi.tuna.tsinghua.edu.cn/simple --trusted-host pypi.tuna.tsinghua.edu.cn torch || \
    echo "torch install skipped (will be pulled by requirements)"
# 安装其余 Python 依赖 (多国内 pip 源依次尝试: 清华→阿里云→腾讯云)
RUN pip install --no-cache-dir -i https://pypi.tuna.tsinghua.edu.cn/simple --trusted-host pypi.tuna.tsinghua.edu.cn -r requirements.txt || \
    pip install --no-cache-dir -i https://mirrors.aliyun.com/pypi/simple --trusted-host mirrors.aliyun.com -r requirements.txt || \
    pip install --no-cache-dir -i https://mirrors.cloud.tencent.com/pypi/simple --trusted-host mirrors.cloud.tencent.com -r requirements.txt

# Pre-cache embedding models
# 1. bge-small-zh-v1.5 for sentence-transformers (RAG)
RUN HF_ENDPOINT=https://hf-mirror.com python3 -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('BAAI/bge-small-zh-v1.5')" || \
    python3 -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('BAAI/bge-small-zh-v1.5')"
# 2. ChromaDB onnx model (all-MiniLM-L6-v2 fallback, ~79MB) — pre-cache to avoid runtime download
RUN python3 -c "from chromadb.utils.embedding_functions import ONNXMiniLM_L6_V2; \
    ef = ONNXMiniLM_L6_V2(preferred_providers=['CPUExecutionProvider']); \
    ef(['pre-cache'])" || echo "chromadb onnx cache skipped"

COPY . .
EXPOSE 8080
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080", "--timeout-keep-alive", "120", "--limit-concurrency", "20"]
