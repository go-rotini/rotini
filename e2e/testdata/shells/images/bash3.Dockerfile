# bash 3.2: macOS's /bin/bash, which has no compopt and doesn't split COMP_WORDS at = or :.
FROM bash:3.2.57@sha256:b6252c62fc86114f93f02104a486c71e6569371d95950ea7479a2ffd6b47f16a
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
