# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20 AS layout
RUN mkdir -p /data/photos && chown 65532:65532 /data/photos

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
COPY --from=layout --chown=nonroot:nonroot /data/photos /data/photos
ENV PHOTO_DIR=/data/photos
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
