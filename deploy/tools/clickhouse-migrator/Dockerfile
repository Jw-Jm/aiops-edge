FROM docker.m.daocloud.io/library/golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /clickhouse-migrator .

FROM docker.m.daocloud.io/library/alpine:3.21
RUN sed -i 's@dl-cdn.alpinelinux.org@mirrors.aliyun.com@g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates
COPY --from=builder /clickhouse-migrator /clickhouse-migrator
ENTRYPOINT ["/clickhouse-migrator"]
