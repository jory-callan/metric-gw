package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"metric-gw/internal/api"
	"metric-gw/internal/buffer"
	"metric-gw/internal/config"
	"metric-gw/internal/flusher"
	"metric-gw/internal/metrics"

	"github.com/labstack/echo/v4"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("config loaded",
		"listen", cfg.Server.Listen,
		"auth_mode", cfg.Auth.Mode,
		"backends", len(cfg.Backends),
		"disk_buffer", cfg.Buffer.Disk.Enabled)

	// 创建自身指标
	met := metrics.New()

	// 创建缓冲管理器
	diskMaxSize, err := cfg.DiskMaxSizeBytes()
	if err != nil {
		slog.Error("invalid disk max_size", "error", err)
		os.Exit(1)
	}
	mgr := buffer.NewManager(buffer.DiskConfig{
		Enabled:  cfg.Buffer.Disk.Enabled,
		BasePath: cfg.Buffer.Disk.Path,
		MaxSize:  diskMaxSize,
	})

	// 为每个后端注册缓冲队列
	for _, b := range cfg.Backends {
		if err := mgr.RegisterBackend(b.Name, cfg.Buffer.Memory.MaxItems); err != nil {
			slog.Error("failed to register backend buffer", "backend", b.Name, "error", err)
			os.Exit(1)
		}
		slog.Info("buffer registered", "backend", b.Name, "type", b.Type, "url", b.URL)
	}

	// 创建并启动 flusher
	flush, err := flusher.New(cfg.Flush, mgr, met, cfg.Backends)
	if err != nil {
		slog.Error("failed to create flusher", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flush.Start(ctx)
	defer flush.Stop()

	// 创建 HTTP server
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = errorHandler

	handler, err := api.NewHandler(cfg, mgr, met)
	if err != nil {
		slog.Error("failed to create API handler", "error", err)
		os.Exit(1)
	}

	authMW := api.AuthMiddleware(cfg.Auth)
	handler.RegisterRoutes(e, authMW)

	// 配置 Echo
	e.Server.ReadTimeout = 10 * time.Second
	e.Server.WriteTimeout = 10 * time.Second
	e.Server.IdleTimeout = 60 * time.Second

	// 启动 HTTP server
	go func() {
		slog.Info("metric-gw starting", "listen", cfg.Server.Listen)
		if err := e.Start(cfg.Server.Listen); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			cancel()
		}
	}()

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("received signal, shutting down", "signal", sig.String())

	// 优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	// flusher.Stop() 会 flush 剩余数据
	slog.Info("metric-gw stopped")
}

// errorHandler 统一错误处理
func errorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError
	msg := err.Error()

	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		if m, ok := he.Message.(string); ok {
			msg = m
		}
	}

	c.JSON(code, map[string]string{"error": msg})
}
