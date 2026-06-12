# Multi-target build for all three graph binaries. One compile stage, one
# image per component (built by deploy.sh with --target):
#
#   docker build --target graph-k8s  -t <registry>/graph-k8s:TAG  .
#   docker build --target graph-otel -t <registry>/graph-otel:TAG .
#   docker build --target graph-read -t <registry>/graph-read:TAG .
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/graph-k8s ./cmd/graph-k8s && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/graph-otel ./cmd/graph-otel && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/graph-read ./cmd/graph-read

FROM alpine:3.21 AS base
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
USER app

FROM base AS graph-k8s
COPY --from=builder /out/graph-k8s .
ENTRYPOINT ["./graph-k8s"]

FROM base AS graph-otel
COPY --from=builder /out/graph-otel .
EXPOSE 8080 4317
ENTRYPOINT ["./graph-otel"]

FROM base AS graph-read
COPY --from=builder /out/graph-read .
EXPOSE 8080
ENTRYPOINT ["./graph-read"]
