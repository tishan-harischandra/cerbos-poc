#!/bin/sh
# Runs as part of the nginx image's own docker-entrypoint.d mechanism,
# alongside its built-in 20-envsubst-on-templates.sh. That script only
# processes /etc/nginx/templates/*.template into nginx's own config dir; it
# never touches the served static files, so assets/env.template.js needs
# this second pass.
set -eu

envsubst '${OIDC_ISSUER} ${OIDC_CLIENT_ID}' \
  < /usr/share/nginx/html/assets/env.template.js \
  > /usr/share/nginx/html/assets/env.js
