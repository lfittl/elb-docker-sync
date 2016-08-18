FROM gliderlabs/alpine:edge

ENV GOPATH /go
ENV CODE_DIR $GOPATH/src/github.com/lfittl/elb-docker-sync

COPY . $CODE_DIR
WORKDIR $CODE_DIR

# We run this all in one layer to reduce the resulting image size
RUN apk-install -t build-deps make curl libc-dev gcc go git tar \
  && apk-install ca-certificates \
  && go build -o /usr/bin/elb-docker-sync \
  && rm -rf $GOPATH \
	&& apk del --purge build-deps

ENTRYPOINT ["/usr/bin/elb-docker-sync"]
