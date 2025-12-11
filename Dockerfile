FROM --platform=$BUILDPLATFORM docker.io/golang:1.24.5-bullseye AS builder
RUN go env -w GO111MODULE=on && go env -w GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o server main.go

FROM docker.io/ubuntu:22.04
RUN sed -i "s|http://archive.ubuntu.com|http://mirrors.aliyun.com|g" /etc/apt/sources.list && \
    sed -i "s|http://security.ubuntu.com|http://mirrors.aliyun.com|g" /etc/apt/sources.list

ENV TZ=Asia/Shanghai
RUN apt-get update && \
    apt-get install -y --no-install-recommends tzdata ca-certificates && update-ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/server /app/server
COPY config.yaml /app/config.yaml
COPY tokens.json /app/tokens.json

EXPOSE 8000 8001

CMD ["/app/server"]
