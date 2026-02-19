ARG GOLANG_VERSION=1.24
ARG ALPINE_VERSION=3.22

FROM golang:${GOLANG_VERSION}-alpine AS builder
RUN apk add --no-cache make
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY internal ./internal
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /build/sandbox-code-executor .

FROM alpine:${ALPINE_VERSION}
COPY --from=builder /build/sandbox-code-executor /usr/local/bin/
ENV GIN_MODE=release
ENTRYPOINT ["sandbox-code-executor"]
