FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:b7a8dcf015beefc613dd26068d2e45d4669a4a9b804eccde7e04908e58a78971
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
