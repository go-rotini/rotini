# bash 3.2: macOS's /bin/bash, which has no compopt and doesn't split COMP_WORDS at = or :.
FROM bash:3.2.57@sha256:0fd7cb8499c63a3c9345e7088a9cd83bb69f6e895e83833859aff838a0312091
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
