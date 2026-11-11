# Pulse Dockerfile
# Multi-stage build for a single binary that can run as any role.

FROM golang:alpine AS build

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache modules
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.version=${VERSION} -s -w" \
    -o /out/pulse ./cmd/pulse

# -----------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/pulse /pulse
COPY --from=build /src/migrations /migrations
COPY --from=build /src/configs /configs

USER nonroot:nonroot

ENTRYPOINT ["/pulse"]
CMD ["dev"]

EXPOSE 32001 32003 32011

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/pulse", "--help"]