FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ai-llm-egress-proxy .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ai-llm-egress-proxy /ai-llm-egress-proxy
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ai-llm-egress-proxy"]
