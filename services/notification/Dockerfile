# syntax=docker/dockerfile:1.6
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY pkg ./pkg
COPY proto ./proto
COPY services/notification ./services/notification
WORKDIR /src/services/notification
RUN apk add --no-cache git && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/notification ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/notification /notification
USER nonroot:nonroot
EXPOSE 9196
ENTRYPOINT ["/notification"]
