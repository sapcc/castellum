FROM golang:1.12-alpine as builder
RUN apk add --no-cache make gcc musl-dev

COPY . /src
RUN make -C /src install PREFIX=/pkg

################################################################################

FROM alpine:latest
MAINTAINER "Stefan Majewsky <stefan.majewsky@sap.com>"

ENTRYPOINT ["/usr/bin/castellum"]
COPY --from=builder /pkg/ /usr/
