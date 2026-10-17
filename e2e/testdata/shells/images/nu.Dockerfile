FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:fa23e411a1c1c8e6c155aab6e618238adb646d0f4653a98735dfc9c669e635d6
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
