FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ai-action-executor .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ai-action-executor /ai-action-executor
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ai-action-executor"]
