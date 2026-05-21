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
RUN test -d /src/proto/gen/notification/v1 || (echo "ERROR: buf generate failed" && ls -la /src/proto/gen/ 2>/dev/null && exit 1) \
 && echo "✓ Proto generation successful"

# ---------- Stage 2: Build service binary ----------
FROM golang:1.26.3-alpine AS build
WORKDIR /src
# Module mode (no go.work) — replace directives di go.mod handle pkg + proto.
COPY pkg ./pkg
COPY --from=proto-gen /src/proto ./proto
COPY services/notification ./services/notification
WORKDIR /src/services/notification
RUN apk add --no-cache git && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/notification ./cmd

# ---------- Stage 3: Runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/notification /notification
# Copy templates folder ke /templates. Set NOTIFICATION_TEMPLATE_DIR=/templates
# di Helm values supaya service nge-resolve path-nya ke lokasi ini (default
# fallback "services/notification/templates" relative to working dir gak exist
# di distroless).
COPY --from=build /src/services/notification/templates /templates
USER nonroot:nonroot
EXPOSE 9196
ENTRYPOINT ["/notification"]
