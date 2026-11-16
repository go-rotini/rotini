# bash 4.4: the oldest bash that shows completion messages and keeps a completer's order.
FROM bash:4.4.23@sha256:2ae8e461b944ae9b7cb036aabc93bb5d1e6c9fc324c943efded4403db252cfe9
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
