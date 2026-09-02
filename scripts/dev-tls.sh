#!/usr/bin/env bash
# Generates deploy/tls/dev-cert.pem and dev-key.pem with mkcert, covering
# every host a browser talks to under docker-compose.tls.yml: localhost,
# 127.0.0.1, ::1 and all three *.localtest.me tenant subdomains.
#
# Idempotent: re-running with the cert already present and still valid for
# every name below is a no-op, so `make up-tls` can call this every time
# with no rebuild cost on a warm checkout.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

command -v mkcert >/dev/null 2>&1 || {
  echo "dev-tls: mkcert is required - https://github.com/FiloSottile/mkcert#installation" >&2
  exit 1
}

TLS_DIR="deploy/tls"
CERT_FILE="${TLS_DIR}/dev-cert.pem"
KEY_FILE="${TLS_DIR}/dev-key.pem"
HOSTS=(localhost 127.0.0.1 ::1 tenant-a.localtest.me tenant-b.localtest.me tenant-c.localtest.me)
# openssl's own subjectAltName text form is what the idempotency check reads
# against, not the mkcert argument list verbatim: it renders "::1" as
# "0:0:0:0:0:0:0:1", so the DNS names are checked by their own text and the
# loopback literals are assumed present together, never checked apart.
DNS_HOSTS=(localhost tenant-a.localtest.me tenant-b.localtest.me tenant-c.localtest.me)

mkdir -p "${TLS_DIR}"

if [[ -f "${CERT_FILE}" ]]; then
  missing=""
  for host in "${DNS_HOSTS[@]}"; do
    openssl x509 -in "${CERT_FILE}" -noout -ext subjectAltName 2>/dev/null | grep -qF "${host}" \
      || missing="${missing} ${host}"
  done
  if [[ -z "${missing}" ]]; then
    echo "dev-tls: ${CERT_FILE} already covers every required host"
    exit 0
  fi
  echo "dev-tls: regenerating - missing:${missing}"
fi

mkcert -cert-file "${CERT_FILE}" -key-file "${KEY_FILE}" "${HOSTS[@]}"

# mkcert writes the key 0600, readable only by the host user. The containers
# that mount it (Keycloak, admin-service, business-ui's nginx) run as their
# own image users, which is never that uid under rootless Podman/Docker, so
# a mode this strict is an AccessDeniedException at container startup, not
# extra safety - this is a local, throwaway dev certificate, never a
# production credential.
chmod 644 "${KEY_FILE}"

CAROOT="$(mkcert -CAROOT)"
cat <<EOF

dev-tls: certificate written to ${CERT_FILE} / ${KEY_FILE}.

For your browser to trust it rather than showing a warning on every
tenant subdomain, install mkcert's local CA once:

  mkcert -install

If that reports it could not reach the system trust store or NSS
(no root, or no certutil), import the CA by hand instead:

  ${CAROOT}/rootCA.pem

EOF
