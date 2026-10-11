# bash 4.4: the oldest bash that shows completion messages and keeps a completer's order.
FROM bash:4.4.23@sha256:45232ab46b7e1c069f8bc9993ca2d2d9fa20b4b4a480699fb0cd2df87629cebf
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
