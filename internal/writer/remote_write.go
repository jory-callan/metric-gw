package writer

import (
	"fmt"

	"metric-gw/pkg/model"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

// EncodeRemoteWrite 将 metrics 编码为 Prometheus remote_write 格式
// protobuf TimeSeries + snappy 压缩
func EncodeRemoteWrite(metrics []model.Metric) ([]byte, error) {
	// 按 name+labels 分组，合并同 series 的时间戳+值
	seriesMap := make(map[string]*prompb.TimeSeries)

	for _, m := range metrics {
		if m.Name == "" {
			continue
		}

		key := seriesKey(m)
		ts, ok := seriesMap[key]
		if !ok {
			labels := make([]prompb.Label, 0, len(m.Labels)+1)
			labels = append(labels, prompb.Label{Name: "__name__", Value: m.Name})
			for k, v := range m.Labels {
				labels = append(labels, prompb.Label{Name: k, Value: v})
			}
			ts = &prompb.TimeSeries{Labels: labels}
			seriesMap[key] = ts
		}
		tsMs := m.Timestamp * 1000
		ts.Samples = append(ts.Samples, prompb.Sample{
			Value:     m.Value,
			Timestamp: tsMs,
		})
	}

	series := make([]prompb.TimeSeries, 0, len(seriesMap))
	for _, ts := range seriesMap {
		series = append(series, *ts)
	}

	req := &prompb.WriteRequest{Timeseries: series}
	data, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf: %w", err)
	}

	compressed := snappy.Encode(nil, data)
	return compressed, nil
}

func seriesKey(m model.Metric) string {
	// 简单用 name + labels 拼接做 key，顺序无关性已由 label 排序保证
	// 这里不追求精确去重，同 batch 内相同 series 会被合并
	return m.Name + "|" + fmt.Sprintf("%v", m.Labels)
}
