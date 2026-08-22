FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG COMMIT=unknown

RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/mqtt2prometheus ./cmd/mqtt2prometheus

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/slakje-nl/mqtt2prometheus" \
      org.opencontainers.image.description="Export selected MQTT messages as Prometheus metrics" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/mqtt2prometheus /mqtt2prometheus

USER nonroot:nonroot
EXPOSE 9000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/mqtt2prometheus", "--healthcheck"]

ENTRYPOINT ["/mqtt2prometheus"]
CMD ["--config", "/config"]
