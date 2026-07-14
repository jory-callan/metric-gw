package api

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsHandler 返回 Prometheus metrics HTTP handler
func (h *Handler) metricsHandler() http.Handler {
	return promhttp.HandlerFor(h.met.Registry(), promhttp.HandlerOpts{})
}
