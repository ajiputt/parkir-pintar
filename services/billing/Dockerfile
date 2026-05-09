# syntax=docker/dockerfile:1.6
FROM golang:1.24-alpine AS build
WORKDIR /src
# Module mode (no go.work) — replace directives di go.mod handle pkg + proto.
COPY pkg ./pkg
COPY proto ./proto
COPY services/billing ./services/billing

WORKDIR /src/services/billing
RUN apk add --no-cache git \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/billing ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/billing /billing
USER nonroot:nonroot
EXPOSE 9092 9192
ENTRYPOINT ["/billing"]
