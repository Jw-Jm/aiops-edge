FROM docker.m.daocloud.io/library/golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=local
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
# 离线构建：依赖已 go mod vendor 入库，构建零网络（vendor 由 fetch-offline-deps.sh 维护）
COPY go.mod go.sum* ./
COPY vendor ./vendor
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /query-api-http ./cmd/api/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /query-run-dispatcher ./cmd/run-dispatcher/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /query-alert-evaluator ./cmd/alert-evaluator/

FROM docker.m.daocloud.io/library/alpine:3.21
# query-api 需要 kubectl 与集群交互（clusters/infrastructure/system 模块 exec kubectl）。
# kubectl 从本地 docker/ 目录构建时 COPY 进镜像，避免外部下载依赖。
# apk 走国内源（阿里云），加速且避免官方源不可达。
RUN sed -i 's@dl-cdn.alpinelinux.org@mirrors.aliyun.com@g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates
COPY docker/kubectl /usr/local/bin/kubectl
COPY --from=builder /query-api-http /query-api-http
COPY --from=builder /query-run-dispatcher /query-run-dispatcher
COPY --from=builder /query-alert-evaluator /query-alert-evaluator
RUN chmod +x /usr/local/bin/kubectl && kubectl version --client -o yaml >/dev/null 2>&1 || true
EXPOSE 8080
ENTRYPOINT ["/query-api-http"]
