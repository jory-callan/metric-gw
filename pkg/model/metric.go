package model

// Metric 单条指标数据
type Metric struct {
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels,omitempty"`
	Value     float64           `json:"value"`
	Timestamp int64             `json:"timestamp,omitempty"` // unix seconds, 0 = server time
}

// MetricsRequest HTTP push 请求体
type MetricsRequest struct {
	Metrics []Metric `json:"metrics"`
}

// BackendStatus 后端状态信息
type BackendStatus struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Healthy     bool   `json:"healthy"`
	MemoryDepth int    `json:"memory_depth"`
	DiskDepth   int64  `json:"disk_depth"`
	DiskSize    int64  `json:"disk_size_bytes"`
}

// StatusResponse 全局状态响应
type StatusResponse struct {
	Backends []BackendStatus `json:"backends"`
}
