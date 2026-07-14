#!/bin/bash
# install.sh - metric-gw K8s 部署（幂等）
# 本地执行时通过环境变量注入 API_KEY，未设置则用默认值
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACE="metric-gw"

# 敏感配置: 优先环境变量，默认值仅用于本地测试
export API_KEY="${API_KEY:-change-me-in-production}"
export IMAGE="${IMAGE:-192.168.5.103:5001/admin/metric-gw:latest}"
export CONFIG_HASH=$(echo -n "${API_KEY}" | sha256sum | awk '{print $1}')

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo ">>> 部署 metric-gw..."

# 1. 渲染所有 yaml (secret 先渲染，其余用 hash)
envsubst < "$SCRIPT_DIR/secret.yaml" > "$TMP_DIR/secret.yaml"
for f in namespace.yaml pvc.yaml deployment.yaml service.yaml ingress.yaml; do
  envsubst < "$SCRIPT_DIR/$f" > "$TMP_DIR/$f"
done

# 2. 按顺序 apply
for f in namespace.yaml secret.yaml pvc.yaml deployment.yaml service.yaml ingress.yaml; do
  echo ">>> apply $f"
  kubectl apply -f "$TMP_DIR/$f"
done

echo ""
echo ">>> 等待 Pod 就绪..."
kubectl -n "$NAMESPACE" rollout status deploy/app-metric-gw --timeout=120s

echo ""
echo "============================================"
echo "✅ metric-gw 部署完成"
echo "============================================"
echo ""
echo "   访问地址:"
echo "   HTTP API:  https://metric-gw.czw-sre.internal"
echo "   内部地址:  http://metric-gw.metric-gw:9201"
echo ""
echo "   推送指标示例:"
echo "   curl -H \"X-API-Key: \$API_KEY\" \\"
echo "     https://metric-gw.czw-sre.internal/api/v1/metrics \\"
echo "     -d '{\"metrics\":[{\"name\":\"test\",\"value\":1}]}'"
echo ""
echo "   查看 Pod:  kubectl -n $NAMESPACE get pods"
echo ""
