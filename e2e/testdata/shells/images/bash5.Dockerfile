FROM bash:5.3.20@sha256:ab2ada570d744f42f517986fdd3c81ef60fe2054ed0c5295a7922e3b0ccd1b02
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
