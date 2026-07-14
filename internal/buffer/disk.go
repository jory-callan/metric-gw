package buffer

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"metric-gw/pkg/model"
)

// segment 格式:
// 每条记录: [4字节长度][JSON payload]
// segment 文件名: 000000000001.wal, 000000000002.wal, ...
// checkpoint 文件: 记录已确认 flush 的 segment 序号 + offset

const (
	segmentMaxSize  = 64 * 1024 * 1024 // 64MB per segment
	segmentExt      = ".wal"
	checkpointFile  = "checkpoint"
)

// DiskQueue 磁盘 WAL 队列 (segment 文件实现)
type DiskQueue struct {
	mu       sync.Mutex
	dir      string
	maxSize  int64

	// 读指针
	readSeg    int64
	readOffset int64

	// 写指针
	writeSeg    int64
	writeFile   *os.File
	writeSize   int64
	totalSize   atomic.Int64
	totalCount  atomic.Int64
}

// NewDiskQueue 创建磁盘队列，扫描已有 segment 恢复状态
func NewDiskQueue(dir string, maxSize int64) *DiskQueue {
	dq := &DiskQueue{
		dir:     dir,
		maxSize: maxSize,
	}

	dq.recover()
	return dq
}

// recover 扫描已有 segment 文件和 checkpoint，恢复读写位置
func (dq *DiskQueue) recover() {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	// 读取 checkpoint
	confirmedSeg := int64(0)
	if data, err := os.ReadFile(filepath.Join(dq.dir, checkpointFile)); err == nil {
		fmt.Sscanf(string(data), "%d", &confirmedSeg)
	}

	// 扫描所有 segment 文件
	entries, err := os.ReadDir(dq.dir)
	if err != nil {
		return
	}

	var segments []int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, segmentExt) {
			continue
		}
		var num int64
		if _, err := fmt.Sscanf(name, "%012d"+segmentExt, &num); err != nil {
			continue
		}
		if num >= confirmedSeg {
			segments = append(segments, num)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })

	if len(segments) == 0 {
		dq.readSeg = 1
		dq.writeSeg = 1
		dq.openWriteSegment()
		return
	}

	// 读指针 = 最旧 segment
	dq.readSeg = segments[0]
	dq.readOffset = 0

	// 写指针 = 最新 segment + 1 (或继续写最新 segment)
	dq.writeSeg = segments[len(segments)-1]
	lastPath := filepath.Join(dq.dir, fmt.Sprintf("%012d%s", dq.writeSeg, segmentExt))
	if info, err := os.Stat(lastPath); err == nil {
		dq.writeSize = info.Size()
	}

	// 统计磁盘占用和条目数
	for _, seg := range segments {
		path := filepath.Join(dq.dir, fmt.Sprintf("%012d%s", seg, segmentExt))
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		dq.totalSize.Add(info.Size())
		// 统计条目数
		if count, err := countRecords(path); err == nil {
			dq.totalCount.Add(int64(count))
		}
	}

	dq.openWriteSegment()

	// 如果恢复的 segment 小于 confirmedSeg，删除已确认的
	for i := int64(0); i < confirmedSeg && i < segments[0]; i++ {
		path := filepath.Join(dq.dir, fmt.Sprintf("%012d%s", i, segmentExt))
		os.Remove(path)
	}
}

// openWriteSegment 打开当前写 segment 文件 (追加模式)
func (dq *DiskQueue) openWriteSegment() {
	path := filepath.Join(dq.dir, fmt.Sprintf("%012d%s", dq.writeSeg, segmentExt))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	dq.writeFile = f
}

// Write 写入一条 metric 到磁盘
func (dq *DiskQueue) Write(m model.Metric) error {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	if dq.totalSize.Load() >= dq.maxSize {
		return fmt.Errorf("disk queue full: %d >= %d", dq.totalSize.Load(), dq.maxSize)
	}

	// 检查是否需要滚动 segment
	if dq.writeFile == nil || dq.writeSize >= segmentMaxSize {
		dq.writeSeg++
		dq.writeSize = 0
		dq.openWriteSegment()
	}

	// 编码: [4字节长度][JSON payload]
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal metric: %w", err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))

	n, err := dq.writeFile.Write(lenBuf[:])
	written := int64(n)
	if err == nil {
		n2, err2 := dq.writeFile.Write(payload)
		written += int64(n2)
		if err2 != nil {
			err = err2
		}
	}

	if err != nil {
		return fmt.Errorf("write to segment: %w", err)
	}

	dq.writeSize += written
	dq.totalSize.Add(written)
	dq.totalCount.Add(1)
	return nil
}

// Read 从磁盘批量读取 metrics
func (dq *DiskQueue) Read(max int) ([]model.Metric, error) {
	dq.mu.Lock()
	defer dq.mu.Unlock()

	result := make([]model.Metric, 0, max)

	for len(result) < max {
		// 如果当前写 segment 没有数据可读，检查是否有未读 segment
		if dq.readSeg > dq.writeSeg {
			break
		}

		path := filepath.Join(dq.dir, fmt.Sprintf("%012d%s", dq.readSeg, segmentExt))
		f, err := os.Open(path)
		if err != nil {
			// 读 segment 不存在，说明已被清理
			break
		}

		// seek 到读 offset
		if _, err := f.Seek(dq.readOffset, io.SeekStart); err != nil {
			f.Close()
			return result, err
		}

		// 读取一条记录
		var lenBuf [4]byte
		_, err = io.ReadFull(f, lenBuf[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			// 当前 segment 读完了
			f.Close()
			if dq.readSeg < dq.writeSeg {
				// 还有后续 segment，删除当前并前进
				os.Remove(path)
				dq.readSeg++
				dq.readOffset = 0
				continue
			}
			// 当前就是最新写 segment，等待新数据
			break
		}
		if err != nil {
			f.Close()
			return result, err
		}

		payloadLen := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(f, payload); err != nil {
			f.Close()
			return result, err
		}

		var m model.Metric
		if err := json.Unmarshal(payload, &m); err != nil {
			f.Close()
			return result, fmt.Errorf("unmarshal metric: %w", err)
		}

		result = append(result, m)

		// 更新读 offset
		pos, _ := f.Seek(0, io.SeekCurrent)
		dq.readOffset = pos
		f.Close()

		// 更新统计
		recordSize := int64(4 + int(payloadLen))
		dq.totalSize.Add(-recordSize)
		dq.totalCount.Add(-1)
	}

	return result, nil
}

// Depth 返回磁盘队列当前条目数
func (dq *DiskQueue) Depth() int64 {
	return dq.totalCount.Load()
}

// Size 返回磁盘队列当前占用字节
func (dq *DiskQueue) Size() int64 {
	return dq.totalSize.Load()
}

// Close 关闭磁盘队列
func (dq *DiskQueue) Close() error {
	dq.mu.Lock()
	defer dq.mu.Unlock()
	if dq.writeFile != nil {
		return dq.writeFile.Close()
	}
	return nil
}

// countRecords 统计 segment 文件中的记录数
func countRecords(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	for {
		var lenBuf [4]byte
		_, err := io.ReadFull(f, lenBuf[:])
		if err != nil {
			break
		}
		payloadLen := binary.BigEndian.Uint32(lenBuf[:])
		if _, err := io.CopyN(io.Discard, f, int64(payloadLen)); err != nil {
			break
		}
		count++
	}
	return count, nil
}
