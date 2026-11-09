FROM bash:5.3.20@sha256:bfe8f6d356f2944ec5ad456e44620924862263aa0717036b3c2cdce0ee4c0af0
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
