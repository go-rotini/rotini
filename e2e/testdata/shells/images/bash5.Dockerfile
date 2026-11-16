FROM bash:5.3.20@sha256:589adbc588d8ef3c1ec3c68333faf4baf5dccb65d3cb50f6cf3d78cad2d6e1b0
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
