FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:5be20e6a14032675adcda153aa88e3330384d9a64a26f8e4af8efbf5b65529f1
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
