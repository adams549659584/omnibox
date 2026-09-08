// MorphoTV 轻量代理 - Go 版本
// 完全对齐原 Express 代理 (server/src/index.ts) 的功能。
//
// 功能：
//   - 动态转发任意 URL：GET /proxy/<编码后的目标URL>
//   - 支持所有 HTTP 方法 (GET/POST/PUT/DELETE/PATCH/HEAD/OPTIONS)
//   - 透传请求体 (非 GET) 与查询参数
//   - 伪装 Chrome UA，绕过部分反爬
//   - 自动补 CORS 头 (预检 OPTIONS 直接返回 204)
//   - 50MB 请求体限制
//   - 透传目标服务器的状态码与响应体
//   - 健康检查：GET /
//
// 编译（Windows）:
//   go build -o morphotv-proxy.exe main.go
//
// 运行:
//   morphotv-proxy.exe
//   PORT=8080 morphotv-proxy.exe   # 指定端口
//
// 前端 PROXY_BASE_URL 填:
//   http://<内网IP>:8080/proxy/
package main

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// 请求体大小限制，对齐 Express 的 50mb
	maxBodySize = 50 << 20 // 50 MB

	// 目标服务器超时
	proxyTimeout = 30 * time.Second

	// 伪装浏览器 UA，对齐 Express 里的 Chrome/91.0.4472.124
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
)

func main() {
	port := getenv("PORT", "8080")

	http.HandleFunc("/", handle)

	log.Printf("MorphoTV Go proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handle(w http.ResponseWriter, r *http.Request) {
	// 根路径 - 健康检查，对齐 Express 的 GET /
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(`{"status":"running","message":"MorphoTV Proxy Server is running","proxyEndpoint":"/proxy/","usage":"Use /proxy/{target-url} to proxy requests"}`))
		return
	}

	// 只处理 /proxy/ 前缀
	if !strings.HasPrefix(r.URL.Path, "/proxy/") {
		http.NotFound(w, r)
		return
	}

	// 取出并解码目标 URL，对齐 Express 的 decodeURIComponent(req.path.replace("/proxy/",""))
	target := strings.TrimPrefix(r.URL.Path, "/proxy/")
	decoded, err := url.PathUnescape(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target url")
		return
	}
	if !strings.HasPrefix(decoded, "http://") && !strings.HasPrefix(decoded, "https://") {
		writeError(w, http.StatusBadRequest, "target url must be http(s)")
		return
	}

	proxy(w, r, decoded)
}

func proxy(w http.ResponseWriter, r *http.Request, target string) {
	// OPTIONS 预检直接返回，不转发，对齐 cors() 的行为
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 限制请求体大小，对齐 express.json({limit:'50mb'})
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	// 构建转发请求
	req, err := http.NewRequest(r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 复制请求头，去掉 host/connection
	for k, vv := range r.Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.Header.Del("Host")
	req.Header.Del("Connection")

	// 伪装浏览器 UA，对齐 Express
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	// 透传查询参数 (对齐 axios params 逻辑)
	req.URL.RawQuery = r.URL.RawQuery

	// 发送请求
	client := &http.Client{Timeout: proxyTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// 对齐 Express：转发目标服务器错误状态码，否则 500
		writeError(w, http.StatusBadGateway, "proxy error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	// 补 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

	// 透传状态码与响应体
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	io.WriteString(w, `{"error":"`+msg+`"}`)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
