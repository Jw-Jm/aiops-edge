FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /event-collector .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates ipmitool
COPY --from=builder /event-collector /event-collector
EXPOSE 8080
ENTRYPOINT ["/event-collector"]
