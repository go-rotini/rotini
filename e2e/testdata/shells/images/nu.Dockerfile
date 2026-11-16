FROM ghcr.io/nushell/nushell:0.117.0-alpine@sha256:b67e78873c72be061d80465916ae233d0854afb8012e0af1aef875fe5f6f099d
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
