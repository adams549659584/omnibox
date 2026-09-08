# MorphoTV 内网部署（nginx 前端 + Go 代理）

纯内网、最轻量的部署方案：**一个 nginx 容器跑前端静态文件，一个 Go 单二进制容器做代理**。浏览器只访问 nginx（同源），nginx 把 `/proxy/` 转发给 Go 代理，因此**不触发跨域**，也**不需要任何公网代理服务**。

## 架构

```
浏览器 ──> nginx (:7180, 静态前端 + SPA 回退)
              │
              └── /proxy/<url> ──> Go 代理 (:8080, 动态转发任意 URL) ──> 采集站
```

- 前端：`Dockerfile` 多阶段构建（bun 构建 → nginx 静态）
- 代理：`deploy-proxy/go-proxy`（Go 单二进制，零依赖）
- 所有服务通过 `docker-compose.yml` 编排

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
> 端口 7180 与 `docker-compose.yml` 里 `web` 的映射端口一致。

点击「导入JSON数据」，系统自动重新加载。

## 验证代理

```bash
# 测试 Go 代理是否正常
curl "http://<内网IP>:8080/proxy/https://httpbin.org/get"

# 通过 nginx 转发测试（同源路径）
curl "http://<内网IP>:7180/proxy/https://httpbin.org/get"
```

## 文件说明

| 文件 | 作用 |
|------|------|
| `Dockerfile` | 前端多阶段构建（bun 构建 → nginx 静态） |
| `nginx.conf` | 静态文件 + SPA 回退 + `/proxy/` 转发到 Go 代理 |
| `docker-compose.yml` | 编排 `web`（nginx）与 `proxy`（Go）两个服务 |
| `deploy-proxy/go-proxy/main.go` | Go 代理源码（零依赖，单二进制） |
| `deploy-proxy/go-proxy/Dockerfile` | Go 代理多阶段构建 |

## 单独运行 Go 代理（不用 Docker）

如果你只想在 Windows 上直接跑代理，不用容器：

```powershell
cd deploy-proxy\go-proxy
go build -o morphotv-proxy.exe main.go
.\morphotv-proxy.exe
# 指定端口
$env:PORT=8080; .\morphotv-proxy.exe
```

前端 `PROXY_BASE_URL` 填 `http://<内网IP>:8080/proxy/`。

## 端口

| 端口 | 服务 | 说明 |
|------|------|------|
| 7180 | nginx（前端） | 浏览器访问入口 |
| 8080 | Go 代理 | 默认仅在容器内部，nginx 转发用；如需直接访问可取消 `docker-compose.yml` 中 `proxy` 的 `ports` 注释 |

## 修改端口

改 `docker-compose.yml` 里 `web` 的 `ports`（如 `7180:80` → `8080:80`），并同步更新前端 `PROXY_BASE_URL` 中的端口。

## 故障排除

- **代理请求失败**：确认采集站可访问、网络正常；查看 `docker compose logs proxy`。
- **跨域报错**：内网同源部署本不该触发，确认前端 `PROXY_BASE_URL` 填的是 nginx 的地址（7180），而不是代理容器内部地址。
- **端口被占用**：修改 `docker-compose.yml` 的端口映射。
