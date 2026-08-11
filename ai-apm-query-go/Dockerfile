FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=local
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /api ./cmd/api/

FROM alpine:3.21
# query-api 需要 kubectl 与集群交互（clusters/infrastructure/system 模块 exec kubectl）。
# kubectl 从本地 docker/ 目录构建时 COPY 进镜像，避免外部下载依赖。
RUN apk add --no-cache ca-certificates
COPY docker/kubectl /usr/local/bin/kubectl
COPY --from=builder /api /api
RUN chmod +x /usr/local/bin/kubectl && kubectl version --client -o yaml >/dev/null 2>&1 || true
EXPOSE 8080
ENTRYPOINT ["/api"]
