# 使用国内 Docker Hub 镜像代理，避免构建时访问 registry-1.docker.io 超时。
FROM docker.m.daocloud.io/library/golang:1.25-alpine AS builder
WORKDIR /app
# 离线构建：event-collector 零第三方依赖（纯标准库），无需网络下载
ENV GOTOOLCHAIN=local
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /event-collector .

FROM docker.m.daocloud.io/library/alpine:3.21
# apk 走国内源（阿里云），避免官方源不可达
RUN sed -i 's@dl-cdn.alpinelinux.org@mirrors.aliyun.com@g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates ipmitool
COPY --from=builder /event-collector /event-collector
EXPOSE 8080
ENTRYPOINT ["/event-collector"]
