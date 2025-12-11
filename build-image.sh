#!/bin/bash

# 解析命令行参数
TAG=""
ARCH="amd64"

while [[ $# -gt 0 ]]; do
    case $1 in
        --tag)
            TAG="$2"
            shift 2
            ;;
        --arch)
            ARCH="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--tag TAG] [--arch ARCH]"
            echo "  --tag TAG    Image tag (default: YYYYMMDD-HHMM)"
            echo "  --arch ARCH  Architecture: amd64 or arm64 (default: amd64)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# 验证架构参数
if [[ "$ARCH" != "amd64" && "$ARCH" != "arm64" ]]; then
    echo "Error: Architecture must be 'amd64' or 'arm64'"
    exit 1
fi

# 如果没有指定tag，使用默认值（日期时间）
if [[ -z "$TAG" ]]; then
    TAG=$(date +'%Y%m%d-%H%M')
fi

# 检查 golang:1.24.5-bullseye 镜像是否存在
if ! docker image inspect golang:1.24.5-bullseye >/dev/null 2>&1; then
    echo "Pulling golang:1.24.5-bullseye from daocloud..."
    docker pull m.daocloud.io/docker.io/library/golang:1.24.5-bullseye
    docker tag m.daocloud.io/docker.io/library/golang:1.24.5-bullseye golang:1.24.5-bullseye
fi

# 检查 ubuntu:22.04 镜像是否存在
if ! docker image inspect ubuntu:22.04 >/dev/null 2>&1; then
    echo "Pulling ubuntu:22.04 from daocloud..."
    docker pull m.daocloud.io/docker.io/library/ubuntu:22.04
    docker tag m.daocloud.io/docker.io/library/ubuntu:22.04 ubuntu:22.04
fi

# 设置平台参数
PLATFORM="linux/${ARCH}"
TARGETOS="linux"

# 构建镜像，镜像名称包含架构信息
IMAGE_NAME="silinex-router-gateway-${ARCH}:${TAG}"
docker build --platform=${PLATFORM} -t ${IMAGE_NAME} --build-arg TARGETARCH=${ARCH} --build-arg TARGETOS=${TARGETOS} -f Dockerfile .
echo "Built image:"
echo "${IMAGE_NAME}"
