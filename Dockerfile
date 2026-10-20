FROM golang:1.25-alpine AS build

ARG APP
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test -n "$APP" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/shopstops ./cmd/"$APP"

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shopstops /shopstops
USER nonroot:nonroot
ENTRYPOINT ["/shopstops"]
