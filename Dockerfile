# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN GOPROXY=https://goproxy.cn,https://goproxy.io,direct go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
# Upload directory owned by nonroot (65532). A new named volume mounted at /media copies
# this ownership only when the directory is not empty, hence the placeholder file,
# so the API can write photos without running as root.
RUN mkdir -p /out/media && touch /out/media/.keep

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
COPY --from=build --chown=65532:65532 /out/media /media
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
