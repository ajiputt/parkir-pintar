# syntax=docker/dockerfile:1.6

# ---------- Stage 1: Proto generation ----------
FROM golang:1.26.3-alpine AS proto-gen
WORKDIR /src
RUN apk add --no-cache git
RUN go install github.com/bufbuild/buf/cmd/buf@v1.34.0
COPY proto ./proto
RUN mkdir -p /src/docs/api
WORKDIR /src/proto
RUN buf generate
RUN test -d /src/proto/gen/payment/v1 || (echo "ERROR: buf generate failed" && ls -la /src/proto/gen/ 2>/dev/null && exit 1) \
 && echo "✓ Proto generation successful"

# ---------- Stage 2: Build service binary ----------
FROM golang:1.26.3-alpine AS build
WORKDIR /src
# Module mode (no go.work) — replace directives di go.mod handle pkg + proto.
COPY pkg ./pkg
COPY --from=proto-gen /src/proto ./proto
COPY services/payment ./services/payment
WORKDIR /src/services/payment
RUN apk add --no-cache git \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/payment ./cmd

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/payment /payment
USER nonroot:nonroot
EXPOSE 9093 9193
ENTRYPOINT ["/payment"]
