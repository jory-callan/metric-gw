package buffer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"metric-gw/pkg/model"
)

// Manager 管理所有后端的缓冲队列
type Manager struct {
	mu      sync.RWMutex
	queues  map[string]*Queue // per-backend
	diskCfg DiskConfig
}

// DiskConfig 磁盘队列配置
type DiskConfig struct {
	Enabled  bool
	BasePath string
	MaxSize  int64 // bytes
}

// NewManager 创建缓冲管理器
func NewManager(diskCfg DiskConfig) *Manager {
	return &Manager{
		queues:  make(map[string]*Queue),
		diskCfg: diskCfg,
	}
}

// RegisterBackend 为指定后端创建独立的缓冲队列
func (m *Manager) RegisterBackend(name string, maxMemoryItems int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.queues[name]; exists {
		return fmt.Errorf("backend %q already registered", name)
	}

	var disk *DiskQueue
	if m.diskCfg.Enabled {
		diskPath := filepath.Join(m.diskCfg.BasePath, name)
		if err := os.MkdirAll(diskPath, 0o755); err != nil {
			return fmt.Errorf("create disk path %s: %w", diskPath, err)
		}
		disk = NewDiskQueue(diskPath, m.diskCfg.MaxSize)
	}

	q := &Queue{
		name:      name,
		memory:    make(chan model.Metric, maxMemoryItems),
		maxMemory: maxMemoryItems,
		disk:      disk,
	}
	m.queues[name] = q
	return nil
}

// Push 写入指标到指定后端的缓冲队列
// 返回 error 表示内存满且磁盘也满（应返回 503 给客户端）
func (m *Manager) Push(name string, metrics []model.Metric) error {
	q := m.getQueue(name)
	if q == nil {
		return fmt.Errorf("backend %q not registered", name)
	}
	return q.Push(metrics)
}

// Drain 从指定后端队列中批量读取指标
func (m *Manager) Drain(name string, max int) ([]model.Metric, error) {
	q := m.getQueue(name)
	if q == nil {
		return nil, fmt.Errorf("backend %q not registered", name)
	}
	return q.Drain(max)
}

// MemoryDepth 返回内存队列当前深度
func (m *Manager) MemoryDepth(name string) int {
	q := m.getQueue(name)
	if q == nil {
		return 0
	}
	return len(q.memory)
}

// DiskDepth 返回磁盘队列当前深度
func (m *Manager) DiskDepth(name string) int64 {
	q := m.getQueue(name)
	if q == nil || q.disk == nil {
		return 0
	}
	return q.disk.Depth()
}

// DiskSize 返回磁盘队列当前占用字节
func (m *Manager) DiskSize(name string) int64 {
	q := m.getQueue(name)
	if q == nil || q.disk == nil {
		return 0
	}
	return q.disk.Size()
}

func (m *Manager) getQueue(name string) *Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queues[name]
}

// Close 关闭所有队列
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for _, q := range m.queues {
		if err := q.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ── Queue ───────────────────────────────────────────────

// Queue per-backend 缓冲队列：内存 channel + 磁盘 WAL 溢出
type Queue struct {
	name      string
	memory    chan model.Metric
	maxMemory int
	disk      *DiskQueue

	// 统计
	droppedOverflow atomic.Int64 // 内存满+磁盘满导致的丢弃数
}

// Push 写入指标
func (q *Queue) Push(metrics []model.Metric) error {
	for i := range metrics {
		select {
		case q.memory <- metrics[i]:
			// 写入内存成功
		default:
			// 内存满，溢出到磁盘
			if q.disk != nil {
				if err := q.disk.Write(metrics[i]); err != nil {
					// 磁盘也满了，丢弃
					q.droppedOverflow.Add(1)
					continue
				}
			} else {
				// 无磁盘队列，丢弃
				q.droppedOverflow.Add(1)
			}
		}
	}
	return nil
}

// Drain 批量读取，优先读内存，内存不足时从磁盘补充
func (q *Queue) Drain(max int) ([]model.Metric, error) {
	result := make([]model.Metric, 0, max)

	// 1. 先从内存读
	for len(result) < max {
		select {
		case m := <-q.memory:
			result = append(result, m)
		default:
			goto diskFallback
		}
	}

diskFallback:
	// 2. 内存空了，从磁盘补充
	if q.disk != nil && len(result) < max {
		need := max - len(result)
		fromDisk, err := q.disk.Read(need)
		if err != nil {
			return result, err
		}
		result = append(result, fromDisk...)
	}

	return result, nil
}

// DroppedOverflow 返回溢出丢弃数
func (q *Queue) DroppedOverflow() int64 {
	return q.droppedOverflow.Load()
}

// Close 关闭队列
func (q *Queue) Close() error {
	if q.disk != nil {
		return q.disk.Close()
	}
	return nil
}
