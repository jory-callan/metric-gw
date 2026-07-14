#!/bin/bash
# uninstall.sh - 卸载 metric-gw
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAMESPACE="metric-gw"

echo ">>> 卸载 metric-gw..."

for f in ingress.yaml service.yaml deployment.yaml pvc.yaml secret.yaml namespace.yaml; do
  echo ">>> delete $f"
  kubectl delete -f "$SCRIPT_DIR/$f" --ignore-not-found
done

echo ""
echo "✅ metric-gw 已卸载"
echo ""
echo "   注意: PVC 数据已删除。如需保留数据，手动操作:"
echo "   kubectl -n $NAMESPACE delete pvc metric-gw-data --ignore-not-found"
echo ""
