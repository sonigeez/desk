# Build a static binary (the SQLite driver is pure Go, so no C toolchain is needed),
# then run it in a minimal image that still has the certificates for fetching HTTPS pages.
# The build stage always runs on the build machine's own platform and cross-compiles for the
# target, so a multi-arch build never runs the Go compiler under emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /desk .

FROM gcr.io/distroless/static-debian12
COPY --from=build /desk /desk
ENV DESK_DB=/data/reading.db DESK_ADDR=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/desk"]
