FROM golang:1.24-alpine AS builder
ENV GOTOOLCHAIN=local
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /api ./cmd/api/

FROM alpine:3.21
# query-api 通过 K8s API (HTTP) 采集日志/查基础设施，不需要 kubectl 二进制，避免外部下载依赖
RUN apk add --no-cache ca-certificates
COPY --from=builder /api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
