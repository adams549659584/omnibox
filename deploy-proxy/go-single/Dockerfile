# MorphoTV 单二进制服务器 - 多阶段构建
# 一个容器、一个进程：Go 二进制同时托管前端静态文件 + 代理。
#
# 构建顺序关键：
#   1. 先用 bun 构建前端生成 dist/
#   2. 再编译 Go，用 //go:embed 把 dist/ 打进二进制
#
# 用法：
#   docker build -t morphotv .
#   docker run -d --name morphotv -p 7180:8080 morphotv

# Stage 1: 构建前端（生成 dist/）
FROM docker.m.daocloud.io/node:20-alpine AS frontend

WORKDIR /app

# 安装 bun
RUN npm install -g bun

# 复制依赖清单并安装
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

# 复制源码并构建
COPY . .
RUN bun run build
# 产物在 /app/dist

# Stage 2: 编译 Go 单二进制（embed dist/）
FROM golang:1.22-alpine AS builder

WORKDIR /app

# 复制 Go 源码
COPY deploy-proxy/go-single/main.go .

# 复制前端构建产物（embed 需要 dist/ 存在）
COPY --from=frontend /app/dist ./dist

# 编译成静态单二进制（CGO_ENABLED=0 保证无 glibc 依赖）
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /morphotv main.go

# Stage 3: 最小运行时镜像
FROM alpine:3.20

# 复制编译好的二进制
COPY --from=builder /morphotv /usr/local/bin/morphotv

# 非 root 用户运行
RUN adduser -D -u 1000 appuser
USER appuser

# 暴露端口
EXPOSE 8080

# 启动
ENTRYPOINT ["morphotv"]
