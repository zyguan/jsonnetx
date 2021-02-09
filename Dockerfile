FROM golang:1.15 AS builder

WORKDIR /build
COPY go.sum go.mod /build/
RUN go mod download

ADD . .
RUN make build


FROM debian:buster

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /build/bin/* /usr/local/bin/

# zyguan/jsonnetx:latest
