# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/frigate-telegram-enhanced ./cmd/frigate-telegram-enhanced
# /data exists in the image and belongs to the container's user: a named volume
# mounted on it inherits that, with no chown to do by hand.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/frigate-telegram-enhanced /frigate-telegram-enhanced
# Static FFmpeg, only used when compress_clips is on (clips over 50 MB).
COPY --from=mwader/static-ffmpeg:9.0.2 /ffmpeg /usr/local/bin/ffmpeg
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8431
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/frigate-telegram-enhanced", "-healthcheck"]
ENTRYPOINT ["/frigate-telegram-enhanced"]
CMD ["-config", "/config/config.yml"]
