FROM ubuntu:21.04 AS builder
RUN mkdir -p /tmp/src
WORKDIR /tmp/src
RUN apt update && apt install -y make golang ca-certificates && apt clean
RUN go get github.com/deepmap/oapi-codegen/cmd/oapi-codegen
COPY Makefile ./
COPY go.mod go.sum ./
RUN make mod-download
COPY . .
RUN make

FROM ubuntu:21.04
RUN mkdir -p /opt/dinonce/config
RUN apt update && apt install -y ca-certificates && apt clean
COPY --from=builder /tmp/src/dist/dinonce /opt/dinonce/dinonce
VOLUME /opt/dinonce/config
ENTRYPOINT ["/opt/dinonce/dinonce"]
EXPOSE 5000
