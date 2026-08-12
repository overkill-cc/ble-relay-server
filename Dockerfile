# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/relayd ./cmd/relayd

# distroless static-nonroot: no shell, no package manager, runs as uid 65532.
# relayd reads its cert/key at startup and writes nothing to disk, so it
# needs nothing else at runtime.
FROM gcr.io/distroless/static-debian12:nonroot AS final

COPY --from=build /out/relayd /relayd

EXPOSE 8443
ENTRYPOINT ["/relayd"]
CMD ["-addr", ":8443", "-tls-cert", "/etc/relayd/tls/fullchain.pem", "-tls-key", "/etc/relayd/tls/privkey.pem"]
