# bash 4.4: the oldest bash that shows completion messages and keeps a completer's order.
FROM bash:4.4.23@sha256:f6effab6173cef3e79a5a430498cf4c70a6817f03a25701b24031e21f66938c9
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
