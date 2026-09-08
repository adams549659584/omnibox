// MorphoTV 单二进制服务器 - Go 版本（生产优化版）
// 一个可执行文件同时做两件事：
//   1. 托管前端静态文件（//go:embed 把 dist/ 打进二进制）
//   2. 动态代理 /proxy/<编码后的目标URL>
//
// 生产级优化：
//   - gzip 压缩静态文本资源
//   - 安全响应头
//   - 请求日志中间件
//   - 优雅停机（SIGTERM/SIGINT）
//   - HTTP 服务器读写/空闲超时
//   - 代理连接池复用（keep-alive）
//   - 缓存策略细化（HTML 不缓存，带 hash 资源 immutable）
//
// 无外部依赖，仅标准库。
//
// 编译（Windows）:
//   先构建前端生成 dist/，再编译：
//   bun run build
//   go build -o morphotv.exe main.go
//
// 运行:
//   morphotv.exe
//   PORT=8080 morphotv.exe   # 指定端口
//
// 前端初始化 PROXY_BASE_URL 填:
//   http://<内网IP>:7180/proxy/
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed all:dist
var distFS embed.FS

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

	// 解出嵌入的静态文件（dist/ 子目录）
	content, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	// 用自定义 handler，避免默认 ServeMux 对路径做清洗（会把 /proxy/https:// 折叠成 /proxy/https:/）
	// 从而完全对齐 Express 的 req.path 行为，兼容 %2F 编码和字面 // 两种目标 URL。
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 代理路径
		if strings.HasPrefix(r.URL.Path, "/proxy/") {
			handleProxy(w, r)
			return
		}

		// 健康检查
		if r.URL.Path == "/" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			io.WriteString(w, `{"status":"running","message":"MorphoTV Server is running","proxyEndpoint":"/proxy/"}`)
			return
		}

		// 静态文件 + SPA 回退
		serveStatic(w, r, content)
	})

	// 中间件链：安全头 -> 日志 -> gzip -> 主处理
	handler = withSecurityHeaders(handler)
	handler = withLogging(handler)
	handler = withGzip(handler)

	// HTTP 服务器超时（生产最佳实践，防 slowloris / 连接堆积）
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// 优雅停机：监听 SIGINT/SIGTERM，给在途请求最多 10s 收尾
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Forced shutdown: %v", err)
		}
	}()

	log.Printf("MorphoTV server listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// serveStatic 托管嵌入的静态文件，未命中时回退到 index.html（SPA）。
func serveStatic(w http.ResponseWriter, r *http.Request, content fs.FS) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	data, err := fs.ReadFile(content, path)
	if err != nil {
		// 文件不存在 -> SPA 回退到 index.html
		path = "index.html"
		data, err = fs.ReadFile(content, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}

	// 缓存策略：
	//   - assets/ 下带 hash 的构建资源内容不可变 -> 长缓存 immutable
	//   - index.html 是入口，必须 no-cache，保证更新后浏览器能拿到新版本
	if strings.Contains(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
}

// handleProxy 动态转发任意 URL。
func handleProxy(w http.ResponseWriter, r *http.Request) {
	// OPTIONS 预检直接返回，不转发
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 取出并解码目标 URL，对齐 Express 的 decodeURIComponent(req.path.replace("/proxy/",""))
	// 用 r.URL.EscapedPath() 保留原始 %2F 与字面 //，避免被路径清洗折叠。
	target := strings.TrimPrefix(r.URL.EscapedPath(), "/proxy/")
	decoded, err := url.PathUnescape(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target url")
		return
	}
	if !strings.HasPrefix(decoded, "http://") && !strings.HasPrefix(decoded, "https://") {
		writeError(w, http.StatusBadRequest, "target url must be http(s)")
		return
	}

	// 限制请求体大小
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	req, err := http.NewRequest(r.Method, decoded, r.Body)
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

	// 伪装浏览器 UA
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	// 透传查询参数
	req.URL.RawQuery = r.URL.RawQuery

	// 发送请求（复用连接池，提升并发性能）
	resp, err := proxyClient.Do(req)
	if err != nil {
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

// proxyClient 是复用的 HTTP 客户端，带连接池（keep-alive），避免每次新建连接。
var proxyClient = &http.Client{
	Timeout: proxyTimeout,
	Transport: &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// withGzip 对文本类响应做 gzip 压缩（静态资源 + JSON），大幅减小传输体积。
// 代理透传的响应（视频流等）不压缩，避免破坏流式/二进制内容。
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 代理路径不压缩（透传上游内容）
		if strings.HasPrefix(r.URL.Path, "/proxy/") {
			next.ServeHTTP(w, r)
			return
		}

		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")

		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, Writer: gz}, r)
	})
}

// gzipResponseWriter 把写入内容压缩后写出。
type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.Writer.Write(b)
}

// withSecurityHeaders 添加安全响应头。
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// withLogging 记录每个请求的方法、路径、状态码与耗时。
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

// statusWriter 捕获响应状态码，供日志使用。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
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
