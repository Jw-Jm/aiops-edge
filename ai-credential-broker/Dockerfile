FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go mtls.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags='-s -w' -o /out/credential-broker .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/credential-broker /credential-broker
USER nonroot:nonroot
ENTRYPOINT ["/credential-broker"]
