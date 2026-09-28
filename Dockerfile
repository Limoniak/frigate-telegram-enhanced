# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/frigate-telegram ./cmd/frigate-telegram

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/frigate-telegram /frigate-telegram
USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/frigate-telegram", "-healthcheck"]
ENTRYPOINT ["/frigate-telegram"]
CMD ["-config", "/config/config.yml"]
