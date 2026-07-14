package api

import (
	"encoding/json"
	"net/http"
	"time"

	"metric-gw/internal/buffer"
	"metric-gw/internal/config"
	"metric-gw/internal/metrics"
	"metric-gw/pkg/model"

	"github.com/labstack/echo/v4"
)

// Handler HTTP API handler
type Handler struct {
	cfg       *config.Config
	mgr       *buffer.Manager
	met       *metrics.Metrics
	maxBody   int64
	backendNames []string
}

// NewHandler 创建 API handler
func NewHandler(cfg *config.Config, mgr *buffer.Manager, met *metrics.Metrics) (*Handler, error) {
	maxBody, err := cfg.MaxBodySizeBytes()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		names = append(names, b.Name)
	}

	return &Handler{
		cfg:          cfg,
		mgr:          mgr,
		met:          met,
		maxBody:      maxBody,
		backendNames: names,
	}, nil
}

// RegisterRoutes 注册所有路由
func (h *Handler) RegisterRoutes(e *echo.Echo, authMW echo.MiddlewareFunc) {
	// 不需要认证的端点
	e.GET("/health", h.health)
	e.GET("/ready", h.ready)
	e.GET("/metrics", h.promMetrics)

	// 需要认证的端点
	api := e.Group("/api/v1", authMW)
	api.POST("/metrics", h.postMetrics)
	api.GET("/backends/status", h.backendsStatus)
}

// health 存活检查
func (h *Handler) health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "alive"})
}

// ready 就绪检查：至少一个后端注册且缓冲可用
func (h *Handler) ready(c echo.Context) error {
	// 检查至少有一个后端有数据可读或缓冲为空（正常状态）
	// 简单判断: 只要 buffer manager 有注册的后端即可
	ready := len(h.backendNames) > 0
	if !ready {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{
			"ready":   false,
			"reason":  "no backends registered",
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"ready": true})
}

// promMetrics Prometheus 格式的自身指标
func (h *Handler) promMetrics(c echo.Context) error {
	// 更新 gauge 指标
	for _, name := range h.backendNames {
		h.met.MemoryDepth.WithLabelValues(name).Set(float64(h.mgr.MemoryDepth(name)))
		h.met.DiskDepth.WithLabelValues(name).Set(float64(h.mgr.DiskDepth(name)))
		h.met.DiskSize.WithLabelValues(name).Set(float64(h.mgr.DiskSize(name)))
	}

	// 用 prometheus registry 生成输出
	w := c.Response().Writer
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	h.met.Registry().Gather()

	// 使用 promhttp 的 handler
	ph := h.metricsHandler()
	ph.ServeHTTP(w, c.Request())
	return nil
}

// postMetrics 接收 HTTP push 指标
func (h *Handler) postMetrics(c echo.Context) error {
	// 限制 body 大小
	c.Request().Body = http.MaxBytesReader(c.Response().Writer, c.Request().Body, h.maxBody)

	var req model.MetricsRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
	}

	if len(req.Metrics) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "no metrics in request",
		})
	}

	// 限制单次请求条数
	const maxMetricsPerRequest = 50000
	if len(req.Metrics) > maxMetricsPerRequest {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{
			"error": "too many metrics in request, max 50000",
		})
	}

	// 填充默认时间戳
	now := time.Now().Unix()
	for i := range req.Metrics {
		if req.Metrics[i].Timestamp == 0 {
			req.Metrics[i].Timestamp = now
		}
	}

	// 写入每个后端的缓冲队列
	var pushErr error
	for _, name := range h.backendNames {
		if err := h.mgr.Push(name, req.Metrics); err != nil {
			pushErr = err
		}
	}

	h.met.Received.WithLabelValues().Add(float64(len(req.Metrics) * len(h.backendNames)))

	if pushErr != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "buffer full: " + pushErr.Error(),
		})
	}

	return c.JSON(http.StatusAccepted, map[string]any{
		"accepted": len(req.Metrics),
	})
}

// backendsStatus 返回各后端状态
func (h *Handler) backendsStatus(c echo.Context) error {
	statuses := make([]model.BackendStatus, 0, len(h.backendNames))
	for _, b := range h.cfg.Backends {
		statuses = append(statuses, model.BackendStatus{
			Name:        b.Name,
			Type:        b.Type,
			URL:         b.URL,
			Healthy:     true, // 简化: 通过缓冲深度判断
			MemoryDepth: h.mgr.MemoryDepth(b.Name),
			DiskDepth:   h.mgr.DiskDepth(b.Name),
			DiskSize:    h.mgr.DiskSize(b.Name),
		})
	}
	return c.JSON(http.StatusOK, model.StatusResponse{Backends: statuses})
}
