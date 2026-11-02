# bash 4.4: the oldest bash that shows completion messages and keeps a completer's order.
FROM bash:4.4.23@sha256:0228ae9b1b45529781590c433db9a3be84deab2221f4527d8441b93b195a54a6
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
