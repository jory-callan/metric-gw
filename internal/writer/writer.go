package writer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"metric-gw/pkg/model"
)

// Writer 后端写入接口
type Writer interface {
	Write(ctx context.Context, metrics []model.Metric) error
	Name() string
	Type() string
	URL() string
	Healthy(ctx context.Context) bool
}

// ── vmImportWriter ──────────────────────────────────────

type vmImportWriter struct {
	name   string
	url    string
	client *http.Client
}

func newVMImportWriter(name, url string, timeout time.Duration) *vmImportWriter {
	return &vmImportWriter{
		name: name,
		url:  url,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (w *vmImportWriter) Write(ctx context.Context, metrics []model.Metric) error {
	body := EncodePrometheusText(metrics)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("backend returned status %d", resp.StatusCode)
	}
	return nil
}

func (w *vmImportWriter) Healthy(ctx context.Context) bool {
	// vm_import 端点不支持 GET 探活，用轻量 POST 空批次
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader([]byte("")))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (w *vmImportWriter) Name() string { return w.name }
func (w *vmImportWriter) Type() string { return "vm_import" }
func (w *vmImportWriter) URL() string  { return w.url }

// ── remoteWriteWriter ───────────────────────────────────

type remoteWriteWriter struct {
	name   string
	url    string
	client *http.Client
}

func newRemoteWriteWriter(name, url string, timeout time.Duration) *remoteWriteWriter {
	return &remoteWriteWriter{
		name: name,
		url:  url,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (w *remoteWriteWriter) Write(ctx context.Context, metrics []model.Metric) error {
	body, err := EncodeRemoteWrite(metrics)
	if err != nil {
		return fmt.Errorf("encode remote_write: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("backend returned status %d", resp.StatusCode)
	}
	return nil
}

func (w *remoteWriteWriter) Healthy(ctx context.Context) bool {
	// remote_write 端点只接受 POST，用空批次探活
	body, err := EncodeRemoteWrite(nil)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (w *remoteWriteWriter) Name() string { return w.name }
func (w *remoteWriteWriter) Type() string { return "remote_write" }
func (w *remoteWriteWriter) URL() string  { return w.url }

// New 根据配置创建 Writer
func New(name, typ, url string, timeout time.Duration) (Writer, error) {
	switch typ {
	case "vm_import":
		return newVMImportWriter(name, url, timeout), nil
	case "remote_write":
		return newRemoteWriteWriter(name, url, timeout), nil
	default:
		return nil, fmt.Errorf("unknown writer type: %s", typ)
	}
}

// ── Prometheus text format encoder ──────────────────────

// EncodePrometheusText 将 metrics 编码为 Prometheus exposition text format
func EncodePrometheusText(metrics []model.Metric) []byte {
	var buf bytes.Buffer
	for _, m := range metrics {
		if m.Name == "" {
			continue
		}
		// metric_name{label1="val1",label2="val2"} value timestamp
		buf.WriteString(m.Name)
		if len(m.Labels) > 0 {
			buf.WriteByte('{')
			keys := make([]string, 0, len(m.Labels))
			for k := range m.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i > 0 {
					buf.WriteByte(',')
				}
				buf.WriteString(k)
				buf.WriteString(`="`)
				buf.WriteString(escapeLabelValue(m.Labels[k]))
				buf.WriteString(`"`)
			}
			buf.WriteByte('}')
		}
		buf.WriteByte(' ')
		buf.WriteString(strconv.FormatFloat(m.Value, 'g', -1, 64))
		if m.Timestamp != 0 {
			// Prometheus text format 用毫秒时间戳
			buf.WriteByte(' ')
			buf.WriteString(strconv.FormatInt(m.Timestamp*1000, 10))
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
