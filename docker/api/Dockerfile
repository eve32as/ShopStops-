# Repository-root context. Resolve and review immutable base digests before release.
# Official supported Go tag; satisfies go.mod's minimum Go 1.25.0.
FROM golang:1.26.11-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/shopstops-api ./cmd/shopstops-api

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/shopstops-api /shopstops-api
USER 10001:10001
ENV HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/shopstops-api"]