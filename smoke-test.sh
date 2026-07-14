#!/bin/bash
set -euo pipefail
cd /Users/czw/mygithub/metric-gw

TMPDIR=$(mktemp -d)
mkdir -p "$TMPDIR/data"

cat > "$TMPDIR/config.yaml" << EOF
server:
  listen: ":19201"
  max_body_size: "10MB"
auth:
  mode: apikey
  api_key: "test-key-12345"
buffer:
  memory:
    max_items: 1000
  disk:
    enabled: true
    path: "$TMPDIR/data"
    max_size: "100MB"
flush:
  batch_size: 100
  interval: "1s"
  timeout: "2s"
  retry:
    max_attempts: 1
    backoff: fixed
    initial_delay: "1s"
    max_delay: "1s"
backends:
  - name: "fake-vm"
    type: vm_import
    url: "http://127.0.0.1:39999/api/v1/import"
EOF

go build -o "$TMPDIR/metric-gw" ./cmd/metric-gw
"$TMPDIR/metric-gw" --config "$TMPDIR/config.yaml" &
GW_PID=$!
sleep 2

echo "=== health ==="
curl -s http://localhost:19201/health || true
echo ""

echo "=== 401 no auth ==="
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:19201/api/v1/metrics -d '{"metrics":[]}' || true

echo "=== 401 wrong key ==="
curl -s -o /dev/null -w "%{http_code}\n" -H "X-API-Key: wrong" http://localhost:19201/api/v1/metrics -d '{"metrics":[]}' || true

echo "=== 202 valid ==="
curl -s -w "\n%{http_code}\n" -H "X-API-Key: test-key-12345" \
  http://localhost:19201/api/v1/metrics \
  -d '{"metrics":[{"name":"test_metric","labels":{"env":"test"},"value":42}]}' || true

echo "=== backends status ==="
curl -s -H "X-API-Key: test-key-12345" http://localhost:19201/api/v1/backends/status || true
echo ""

echo "=== /metrics ==="
curl -s http://localhost:19201/metrics | grep -E "metric_gw_received_total|metric_gw_buffer_memory" | head -5 || true
echo ""

sleep 3
kill $GW_PID 2>/dev/null; wait $GW_PID 2>/dev/null
rm -rf "$TMPDIR"
echo "=== Done ==="
