# syntax=docker/dockerfile:1.6
FROM golang:1.24-alpine AS build
WORKDIR /src
# Module mode (no go.work) — replace directives di go.mod handle pkg + proto.
COPY pkg ./pkg
COPY proto ./proto
COPY services/notification ./services/notification
WORKDIR /src/services/notification
RUN apk add --no-cache git && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/notification ./cmd

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
