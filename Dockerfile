# Backend: Go API + scheduler. Static binary (modernc SQLite, no CGO).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scrape ./cmd/scrape \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /out/scrape /usr/local/bin/
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV PR_DB_PATH=/data/priceradar.db \
    PR_LISTEN_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=6s --start-period=10s CMD ["/usr/local/bin/server", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/server"]
