FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:3cd347f3cb5c6cdb06098c33be0fc2e5d3dc603e313d46c31c6fc4fe9ce80351
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
