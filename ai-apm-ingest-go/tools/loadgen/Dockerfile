FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /loadgen ./tools/loadgen/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /loadgen /loadgen
ENTRYPOINT ["/loadgen"]
