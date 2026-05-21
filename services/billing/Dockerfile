# syntax=docker/dockerfile:1.6

# ---------- Stage 1: Proto generation ----------
# Generate proto stubs di-dalam container biar caller ga perlu install Go + buf
# locally. Output: /src/proto/gen/{billing,common,...}/v1/*.pb.go
FROM golang:1.26.3-alpine AS proto-gen
WORKDIR /src
RUN apk add --no-cache git
RUN go install github.com/bufbuild/buf/cmd/buf@v1.34.0
COPY proto ./proto
# buf.gen.yaml openapi plugin output ke ../docs/api/ — create parent dulu
# biar buf generate ga fail silent.
RUN mkdir -p /src/docs/api
WORKDIR /src/proto
RUN buf generate
# Verify generation sukses — fail fast kalau gen/ kosong (catch silent failures).
RUN test -d /src/proto/gen/billing/v1 || (echo "ERROR: buf generate did NOT produce billing stubs" && ls -la /src/proto/gen/ && exit 1) \
 && test -f /src/proto/gen/billing/v1/billing.pb.go || (echo "ERROR: billing.pb.go missing" && exit 1) \
 && echo "✓ Proto generation successful" && ls -la /src/proto/gen/

# ---------- Stage 2: Build service binary ----------
FROM golang:1.26.3-alpine AS build
WORKDIR /src
# Module mode (no go.work) — replace directives di go.mod handle pkg + proto.
COPY pkg ./pkg
# Pakai proto dari stage proto-gen yang udah include generated stubs.
COPY --from=proto-gen /src/proto ./proto
COPY services/billing ./services/billing

# Verify proto stubs ke-copy properly (defensive — kalau --from=proto-gen
# silent fail, error message akan clear).
RUN test -f /src/proto/gen/billing/v1/billing.pb.go || \
    (echo "ERROR: proto stubs NOT copied to build stage" && \
     echo "Contents of /src/proto:" && ls -la /src/proto/ && \
     echo "Contents of /src/proto/gen (kalau ada):" && ls -la /src/proto/gen/ 2>/dev/null && \
     exit 1) \
 && echo "✓ Proto stubs available di build stage"

WORKDIR /src/services/billing
RUN apk add --no-cache git \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/billing ./cmd

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/billing /billing
USER nonroot:nonroot
EXPOSE 9092 9192
ENTRYPOINT ["/billing"]
