package flusher

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"metric-gw/internal/buffer"
	"metric-gw/internal/config"
	"metric-gw/internal/metrics"
	"metric-gw/internal/writer"
	"metric-gw/pkg/model"
)

// Flusher 管理 per-backend flush worker
type Flusher struct {
	cfg         config.FlushConfig
	mgr         *buffer.Manager
	metrics     *metrics.Metrics
	flushIntv   time.Duration
	flushTO     time.Duration
	retryInit   time.Duration
	retryMax    time.Duration
	backendCfgs []config.BackendConfig

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New 创建 flusher
func New(
	cfg config.FlushConfig,
	mgr *buffer.Manager,
	met *metrics.Metrics,
	backendCfgs []config.BackendConfig,
) (*Flusher, error) {
	flushIntv, err := config.ParseDuration(cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("parse flush interval: %w", err)
	}
	flushTO, err := config.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("parse flush timeout: %w", err)
	}
	retryInit, err := config.ParseDuration(cfg.Retry.InitialDelay)
	if err != nil {
		return nil, fmt.Errorf("parse retry initial_delay: %w", err)
	}
	retryMax, err := config.ParseDuration(cfg.Retry.MaxDelay)
	if err != nil {
		return nil, fmt.Errorf("parse retry max_delay: %w", err)
	}

	return &Flusher{
		cfg:         cfg,
		mgr:         mgr,
		metrics:     met,
		flushIntv:   flushIntv,
		flushTO:     flushTO,
		retryInit:   retryInit,
		retryMax:    retryMax,
		backendCfgs: backendCfgs,
	}, nil
}

// Start 启动所有后端的 flush worker
func (f *Flusher) Start(ctx context.Context) {
	ctx, f.cancel = context.WithCancel(ctx)

	for _, bcfg := range f.backendCfgs {
		w, err := writer.New(bcfg.Name, bcfg.Type, bcfg.URL, f.flushTO)
		if err != nil {
			slog.Error("failed to create writer",
				"backend", bcfg.Name,
				"error", err)
			continue
		}
		f.wg.Add(1)
		go f.runWorker(ctx, w)
	}
}

// Stop 停止所有 worker
func (f *Flusher) Stop() {
	if f.cancel != nil {
		f.cancel()
	}
	f.wg.Wait()
}

// runWorker 单个后端的 flush 循环
func (f *Flusher) runWorker(ctx context.Context, w writer.Writer) {
	defer f.wg.Done()

	ticker := time.NewTicker(f.flushIntv)
	defer ticker.Stop()

	slog.Info("flush worker started", "backend", w.Name(), "type", w.Type())

	for {
		select {
		case <-ctx.Done():
			// 退出前尝试 flush 剩余数据
			f.flushRemaining(w)
			return
		case <-ticker.C:
			f.flushBatch(ctx, w)
		}
	}
}

// flushBatch 读取一批指标并写入后端
func (f *Flusher) flushBatch(ctx context.Context, w writer.Writer) {
	batch, err := f.mgr.Drain(w.Name(), f.cfg.BatchSize)
	if err != nil {
		slog.Error("drain from buffer failed",
			"backend", w.Name(), "error", err)
		return
	}
	if len(batch) == 0 {
		return
	}

	f.flushWithRetry(ctx, w, batch)
}

// flushRemaining 退出时 flush 剩余数据
func (f *Flusher) flushRemaining(w writer.Writer) {
	for {
		batch, err := f.mgr.Drain(w.Name(), f.cfg.BatchSize)
		if err != nil {
			slog.Error("drain remaining failed",
				"backend", w.Name(), "error", err)
			return
		}
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), f.flushTO)
		f.flushWithRetry(ctx, w, batch)
		cancel()
	}
}

// flushWithRetry 带重试的 flush
func (f *Flusher) flushWithRetry(ctx context.Context, w writer.Writer, batch []model.Metric) {
	backoff := f.retryInit
	attempt := 0

	for {
		attempt++
		start := time.Now()

		// 用独立 timeout context，不受父 ctx 取消影响（退出时除外）
		flushCtx, cancel := context.WithTimeout(context.Background(), f.flushTO)
		err := w.Write(flushCtx, batch)
		cancel()

		elapsed := time.Since(start)
		f.metrics.FlushDuration.WithLabelValues(w.Name()).Observe(elapsed.Seconds())

		if err == nil {
			f.metrics.Flushed.WithLabelValues(w.Name(), "success").Add(float64(len(batch)))
			return
		}

		f.metrics.Flushed.WithLabelValues(w.Name(), "failed").Add(float64(len(batch)))
		slog.Warn("flush failed, retrying",
			"backend", w.Name(),
			"attempt", attempt,
			"batch_size", len(batch),
			"error", err)

		// 检查是否超过最大重试次数
		if f.cfg.Retry.MaxAttempts > 0 && attempt >= f.cfg.Retry.MaxAttempts {
			slog.Error("flush max attempts exceeded, dropping batch",
				"backend", w.Name(),
				"batch_size", len(batch))
			f.metrics.Dropped.WithLabelValues(w.Name(), "max_retries").Add(float64(len(batch)))
			return
		}

		// 退避等待
		select {
		case <-ctx.Done():
			// 父 context 取消（进程退出），把数据放回缓冲
			slog.Warn("context cancelled during retry, returning batch to buffer",
				"backend", w.Name(), "batch_size", len(batch))
			// 数据已经在 drain 时从队列移除，重新推回
			if err := f.mgr.Push(w.Name(), batch); err != nil {
				slog.Error("failed to return batch to buffer",
					"backend", w.Name(), "error", err)
				f.metrics.Dropped.WithLabelValues(w.Name(), "buffer_return_failed").Add(float64(len(batch)))
			}
			return
		case <-time.After(backoff):
		}

		// 指数退避
		if f.cfg.Retry.Backoff == "exponential" {
			backoff = time.Duration(math.Min(float64(backoff*2), float64(f.retryMax)))
		}
	}
}
