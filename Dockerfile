# syntax=docker/dockerfile:1

# Compile on the builder's native CPU; only the resulting Linux binary is
# architecture-specific. Keep these digests in step with go.mod and base updates.
FROM --platform=$BUILDPLATFORM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
# Keep modules in a regular builder layer: exported CI caches do not preserve
# cache-mount contents when a later source change invalidates the compile step.
# Optional enterprise/session CA trust is a secret, never an image layer.
RUN --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY tools/container-health ./tools/container-health
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
RUN --network=none --mount=type=cache,target=/root/.cache/go-build \
    test "$TARGETOS" = linux && \
    if [ "$TARGETARCH" = arm ]; then export GOARM="${TARGETVARIANT#v}"; fi && \
    export CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOPROXY=off && \
    go build -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w -X main.appVersion=$VERSION" -o /out/entropy-server ./cmd/entropy-server && \
    go build -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w" -o /out/entropy-client ./cmd/entropy-client && \
    go build -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w" -o /out/entropy-health ./tools/container-health && \
    install -d -m 0750 /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
ARG VERSION=dev
ARG VCS_REF=unknown
ARG RECIPE_REF=unknown
ARG SOURCE_URL=https://github.com/fantuz/entropy-service
LABEL org.opencontainers.image.source="$SOURCE_URL" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$VCS_REF" \
      io.entropy-service.recipe.revision="$RECIPE_REF" \
      org.opencontainers.image.licenses="MIT"
COPY LICENSE /usr/share/doc/entropy-service/LICENSE
COPY QUANTIS_NOTICES.txt /usr/share/doc/entropy-service/QUANTIS_NOTICES.txt
COPY cmd/entropy-server/internal/wordlist/NOTICE.txt /usr/share/doc/entropy-service/WORDLIST_NOTICES.txt
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
WORKDIR /data
STOPSIGNAL SIGTERM

FROM runtime AS client
LABEL org.opencontainers.image.title="Entropy Client" \
      org.opencontainers.image.description="HTTP/WebSocket stream analysis, terminal waterfall and bounded exports"
COPY --from=build /out/entropy-client /usr/local/bin/entropy-client
ENTRYPOINT ["/usr/local/bin/entropy-client"]
CMD ["-mode=pull", "-url=http://entropy-server:8080/v1/data/random"]

# The server is also the default target for an ordinary `docker build .`.
FROM runtime AS server
LABEL org.opencontainers.image.title="Entropy Server" \
      org.opencontainers.image.description="Self-hosted DRBG random-data APIs, browser LABs and operational metrics"
COPY --from=build /out/entropy-server /usr/local/bin/entropy-server
COPY --from=build /out/entropy-health /usr/local/bin/entropy-health
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/usr/local/bin/entropy-health"]
EXPOSE 8080 8443
ENTRYPOINT ["/usr/local/bin/entropy-server"]
CMD ["-device=/dev/urandom", "-require-device=true", "-http=:8080", "-enable-https=false"]
