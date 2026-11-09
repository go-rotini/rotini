FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:9e1440d2b78d296364b474b4113623ba1118336592cc1d2a595bb817966998ed
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
