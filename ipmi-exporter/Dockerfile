# ipmi-exporter — 本地 IPMI 传感器采集（/dev/ipmi0，不走带外网）
# 以 privileged + hostPath /dev/ipmi0 运行，用 ipmitool 读 BMC 传感器，管理网上报。
FROM docker.m.daocloud.io/library/debian:bookworm-slim

# apt 切国内源加速
ARG APT_MIRROR=mirrors.tuna.tsinghua.edu.cn
RUN set -eux; \
    DEBIAN_SRC=/etc/apt/sources.list.d/debian.sources; \
    if [ -f "$DEBIAN_SRC" ]; then \
      sed -i "s@deb.debian.org@${APT_MIRROR}@g; s@security.debian.org@${APT_MIRROR}@g" "$DEBIAN_SRC"; \
    fi; \
    apt-get update -qq && apt-get install -y --no-install-recommends \
      ipmitool \
      python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/ipmi-exporter
COPY collect.py .

ENTRYPOINT ["python3", "/opt/ipmi-exporter/collect.py"]
