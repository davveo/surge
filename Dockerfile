# Default to DaoCloud so builds work when docker.io / baidubce mirrors are down.
ARG GO_IMAGE=docker.m.daocloud.io/library/golang:1.20-alpine
ARG ALPINE_IMAGE=docker.m.daocloud.io/library/alpine:3.20
FROM ${GO_IMAGE} AS builder
WORKDIR /src

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOOS=linux \
    GO111MODULE=on

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY pkg ./pkg
COPY proto ./proto
COPY im-core ./im-core
COPY gateway ./gateway

RUN go build -trimpath -ldflags="-s -w" -o /out/im-core ./im-core \
 && go build -trimpath -ldflags="-s -w" -o /out/gateway ./gateway

FROM ${ALPINE_IMAGE} AS im-core
RUN apk add --no-cache ca-certificates tzdata netcat-openbsd
WORKDIR /app
COPY --from=builder /out/im-core /app/im-core
EXPOSE 9000
ENTRYPOINT ["/app/im-core"]

FROM ${ALPINE_IMAGE} AS gateway
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/gateway /app/gateway
COPY web /app/web
ENV WEB_DIR=/app/web
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
