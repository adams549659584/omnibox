# MorphoTV 内网部署（Go 单二进制）

纯内网、最轻量的部署方案：**一个 Go 二进制同时托管前端静态文件 + 做代理**，一个容器、一个进程、零外部依赖。

## 架构

```
浏览器 ──> Go 单二进制 (:7180)
              ├── 托管前端静态文件（//go:embed 打进二进制）
              └── /proxy/<url> ──> 动态转发任意 URL ──> 采集站
```

- 前端 `dist/` 通过 `//go:embed` **嵌入进二进制**，一个 exe 就是完整应用
- 代理逻辑用 Go 标准库实现，**零第三方依赖**

## 生产级优化（已内置）

| 优化 | 说明 |
|------|------|
| **gzip 压缩** | 对静态文本资源（HTML/JS/CSS/JSON）自动 gzip，`Vary: Accept-Encoding`；代理透传的流式/二进制内容不压缩 |
| **HTTP 服务器超时** | `ReadHeaderTimeout` 10s、`ReadTimeout` 30s、`WriteTimeout` 60s、`IdleTimeout` 120s，防 slowloris 与连接堆积 |
| **优雅停机** | 监听 `SIGTERM`/`SIGINT`，给在途请求最多 10s 收尾再退出 |
| **代理连接池** | 复用 keep-alive 连接（`MaxIdleConns` 100 / per-host 20），提升并发性能 |
| **请求日志** | 记录每个请求的方法、路径、状态码、耗时 |
| **安全响应头** | `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`X-XSS-Protection` |
| **缓存策略细化** | 带 hash 的 `/assets/` 资源 `immutable` 长缓存；`index.html` 入口 `no-cache` 保证更新即时生效 |
| **健康检查** | `GET /` 返回 `{"status":"running",...}`，可用于探活 |
| **请求体限制** | 上限 50MB，防恶意超大请求 |
| **UA 伪装** | 转发时设置浏览器 User-Agent，绕过部分采集站的反爬 |

## 快速开始

```bash
# 在项目根目录
docker compose up -d --build
```

访问 `http://<内网IP>:7180`。

## 配置前端代理地址

打开应用，在初始化对话框选择「JSON数据」标签，填入：

```json
{
  "PROXY_BASE_URL": "http://<内网IP>:7180/proxy/"
}
```

> `<内网IP>` 换成运行 docker 的那台机器的局域网 IP（如 `192.168.1.100`）。
> 端口 7180 与 `docker-compose.yml` 里 `morphotv` 的映射端口一致。

点击「导入JSON数据」，系统自动重新加载。

## 验证代理

```bash
curl "http://<内网IP>:7180/proxy/https://httpbin.org/get"
```

## 文件说明

| 文件 | 作用 |
|------|------|
| `deploy-proxy/go-single/main.go` | 单二进制服务器（静态托管 + 代理 + SPA 回退 + gzip + 安全头 + 日志 + 优雅停机） |
| `deploy-proxy/go-single/Dockerfile` | 多阶段构建（bun 构建前端 → go 编译 embed） |
| `docker-compose.yml` | 编排单个 `morphotv` 服务 |

## 本地直接运行（不用 Docker）

```powershell
# 1. 先构建前端生成 dist/
bun run build

# 2. 再编译 Go（embed dist/ 需要它存在）
cd deploy-proxy\go-single
go build -o morphotv.exe main.go

# 3. 运行
.\morphotv.exe
# 指定端口
$env:PORT=8080; .\morphotv.exe
```

前端 `PROXY_BASE_URL` 填 `http://<内网IP>:8080/proxy/`。

## 端口

| 端口 | 说明 |
|------|------|
| 7180 | 浏览器访问入口（compose 映射到容器 8080） |

改端口只需修改 `docker-compose.yml` 里 `ports` 的左侧，并同步更新前端 `PROXY_BASE_URL`。

## 故障排除

- **代理请求失败**：确认采集站可访问、网络正常；查看 `docker compose logs morphotv`。
- **页面空白/404**：确认 `dist/` 已构建（本地跑要先 `bun run build`）；Docker 构建会自动构建。
- **端口被占用**：修改 `docker-compose.yml` 的端口映射。
