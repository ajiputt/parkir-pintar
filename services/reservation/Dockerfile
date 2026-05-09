# syntax=docker/dockerfile:1.6
FROM golang:1.24-alpine AS build
WORKDIR /src

# Copy go workspace files first for better cache.
COPY go.work go.work.sum* ./
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
