FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:a4d8b18a68d629d24d8e70f6616c99f4dc55627ef9c79bac73283fafb892d3cd
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
