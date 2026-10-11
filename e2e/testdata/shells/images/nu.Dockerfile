FROM ghcr.io/nushell/nushell:0.116.1-alpine@sha256:b005693ca54efdd4730924876e2ade4e1ab32166589c387279e3bf6ab8df90bf
USER root
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
ENTRYPOINT []
