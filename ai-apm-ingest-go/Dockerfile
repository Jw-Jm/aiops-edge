FROM docker.m.daocloud.io/library/golang:1.25-alpine AS builder
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod ./
COPY . .
# -trimpath：可复现构建；-ldflags "-s -w"：去掉符号表与 DWARF，精简二进制体积（P15.3）
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ingest ./cmd/ingest/

FROM docker.m.daocloud.io/library/alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ingest /ingest
EXPOSE 8080
ENTRYPOINT ["/ingest"]
