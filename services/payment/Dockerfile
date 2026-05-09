# syntax=docker/dockerfile:1.6
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY pkg ./pkg
COPY proto ./proto
COPY services/payment ./services/payment
WORKDIR /src/services/payment
RUN apk add --no-cache git \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/payment ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/payment /payment
USER nonroot:nonroot
EXPOSE 9093 9193
ENTRYPOINT ["/payment"]
