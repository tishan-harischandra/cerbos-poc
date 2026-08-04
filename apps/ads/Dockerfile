# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src
COPY apps/ads/go.mod apps/ads/go.sum ./
RUN go mod download

COPY apps/ads/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ads ./cmd/ads

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10001 ads
USER ads

COPY --from=build /out/ads /usr/local/bin/ads

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ads"]
