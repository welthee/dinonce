FROM ubuntu:22.04 AS builder
RUN mkdir -p /tmp/src
WORKDIR /tmp/src
RUN apt update && apt install -y make golang ca-certificates && apt clean
RUN go install github.com/deepmap/oapi-codegen/cmd/oapi-codegen@latest
COPY Makefile ./
COPY go.mod go.sum ./
RUN make mod-download
COPY . .
RUN make

FROM ubuntu:22.04
RUN mkdir -p /opt/dinonce
RUN mkdir -p /opt/dinonce/config
RUN adduser --system --disabled-password --home /opt/dinonce --no-create-home dinonce && chown -R dinonce /opt/dinonce
RUN apt update && apt install -y ca-certificates && apt clean
COPY ./scripts /opt/dinonce/scripts
COPY --from=builder /tmp/src/dist/dinonce /opt/dinonce/dinonce
VOLUME /opt/dinonce/config
EXPOSE 5000
WORKDIR /opt/dinonce
USER dinonce
ENTRYPOINT ["/opt/dinonce/dinonce"]
