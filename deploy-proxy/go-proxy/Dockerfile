# MorphoTV Go 代理 - 多阶段构建
# Stage 1: 编译 Go 单二进制
FROM golang:1.22-alpine AS builder

WORKDIR /app

# 复制源码
COPY main.go .

# 编译成静态单二进制 (CGO_ENABLED=0 保证无 glibc 依赖，可在 alpine 运行)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /morphotv-proxy main.go

# Stage 2: 最小运行时镜像
FROM alpine:3.20

# 复制编译好的二进制
COPY --from=builder /morphotv-proxy /usr/local/bin/morphotv-proxy

# 创建非 root 用户运行 (更安全)
RUN adduser -D -u 1000 appuser
USER appuser

# 暴露端口
EXPOSE 8080

# 启动
ENTRYPOINT ["morphotv-proxy"]
