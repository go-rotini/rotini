# bash 3.2: macOS's /bin/bash, which has no compopt and doesn't split COMP_WORDS at = or :.
FROM bash:3.2.57@sha256:44900632c47e54937a3c2d7de2b4d05af7aee6d269c71c3116204842ee26101c
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
