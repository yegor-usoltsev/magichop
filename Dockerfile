FROM alpine:latest
RUN apk add --no-cache --update ca-certificates tini tzdata
ARG TARGETPLATFORM
ENTRYPOINT ["tini", "--"]
CMD ["/magichop", "server"]
COPY $TARGETPLATFORM/magichop /
