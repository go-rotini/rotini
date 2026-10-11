FROM bash:5.3.20@sha256:61962062d969cb46dfc2bad061d36342406fa485f64f246aa7e95693ca07df1f
RUN addgroup -S rotinigroup && adduser -S -D -H -G rotinigroup rotiniuser
