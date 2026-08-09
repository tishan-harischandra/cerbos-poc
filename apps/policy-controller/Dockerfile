# syntax=docker/dockerfile:1
# Build context is the repository root.
FROM docker.io/library/golang:1.25-alpine AS build

WORKDIR /src

# The policy controller depends on libs/policyrelease and libs/leaderlock,
# resolved by the replace directives in its go.mod. Their manifests are
# copied before the sources so a change to controller code alone still reuses
# the cached download layer.
COPY apps/policy-controller/go.mod apps/policy-controller/go.sum ./apps/policy-controller/
COPY libs/policyrelease/go.mod libs/policyrelease/go.sum ./libs/policyrelease/
COPY libs/leaderlock/go.mod libs/leaderlock/go.sum ./libs/leaderlock/
WORKDIR /src/apps/policy-controller
RUN go mod download

COPY libs/policyrelease/ /src/libs/policyrelease/
COPY libs/leaderlock/ /src/libs/leaderlock/
COPY apps/policy-controller/ /src/apps/policy-controller/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/policy-controller ./cmd/policy-controller

# The validation gate runs `cerbos compile` against the fetched commit before
# any archive is built (§13.2), so the cerbos binary that will actually serve
# the release has to be on the controller's own PATH. Copying it out of the
# exact image tag the PDP runs is what guarantees a release can never
# validate against a different cerbos build than the one it ends up
# installed on.
FROM ghcr.io/cerbos/cerbos:0.46.0 AS cerbos

FROM docker.io/library/alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 10001 policy-controller \
  && mkdir -p /var/lib/policy-controller/archives /var/lib/policy-controller/work \
  && chown -R policy-controller:policy-controller /var/lib/policy-controller
USER policy-controller

COPY --from=build /out/policy-controller /usr/local/bin/policy-controller
COPY --from=cerbos /cerbos /usr/local/bin/cerbos

EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/policy-controller"]
