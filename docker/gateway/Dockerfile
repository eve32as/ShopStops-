# Repository-root context. Review immutable base digests before release.
# Pinned supported patch version, satisfies go.mod minimum Go 1.25.0.
FROM golang:1.26.11-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/shopstops-gateway/ ./cmd/shopstops-gateway/
COPY internal/gateway/ ./internal/gateway/
RUN go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/shopstops-gateway ./cmd/shopstops-gateway

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/shopstops-gateway /shopstops-gateway
USER 10001:10001
ENV HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/shopstops-gateway"]