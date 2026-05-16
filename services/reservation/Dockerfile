# syntax=docker/dockerfile:1.6
FROM golang:1.26.3-alpine AS build
WORKDIR /src

# Build dengan module mode (no workspace) supaya Docker image self-contained
# tanpa harus copy semua service. Replace directives di services/X/go.mod
# (../../pkg, ../../proto) handle dependency resolution.
COPY pkg ./pkg
COPY proto ./proto
COPY services/reservation ./services/reservation

WORKDIR /src/services/reservation
RUN apk add --no-cache git \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/reservation ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/reservation /reservation
USER nonroot:nonroot
EXPOSE 9091 9191
ENTRYPOINT ["/reservation"]
