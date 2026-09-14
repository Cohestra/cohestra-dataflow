FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY apps/workflow-go/go.mod apps/workflow-go/go.sum* ./
COPY apps/workflow-go/vendor ./vendor
COPY apps/workflow-go ./
# BUILD_TAGS=ee links the enterprise code under ee/ (Elastic License 2.0);
# build with --build-arg BUILD_TAGS= for a pure AGPL-3.0 community image.
ARG BUILD_TAGS=ee
RUN CGO_ENABLED=0 go build -mod=vendor -tags "$BUILD_TAGS" -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -mod=vendor -tags "$BUILD_TAGS" -trimpath -ldflags="-s -w" -o /out/activity-worker ./cmd/activity-worker && \
    CGO_ENABLED=0 go build -mod=vendor -tags "$BUILD_TAGS" -trimpath -ldflags="-s -w" -o /out/workflow-worker ./cmd/worker

FROM alpine:3.21
# Pull patched OS package revisions (openssl/c-ares CVE fixes land as -rN
# package updates within the 3.21 branch, not as new image tags).
RUN apk -U upgrade --no-cache && addgroup -S dataflow && adduser -S dataflow -G dataflow
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/api /out/activity-worker /out/workflow-worker /usr/local/bin/
USER dataflow
ENTRYPOINT ["workflow-worker"]
