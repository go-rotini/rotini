# bash 3.2: macOS's /bin/bash, which has no compopt and doesn't split COMP_WORDS at = or :.
FROM bash:3.2.57@sha256:2556c2da19c32c1614d318645c73a7bf64d38be21cf69e98e3ca51f053983719
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
