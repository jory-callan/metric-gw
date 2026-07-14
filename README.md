# metric-gw

HTTP 指标推送网关 - 接收应用 push 的 JSON 指标，经内存+磁盘双缓冲后批量转发到 VictoriaMetrics / Prometheus 等多后端。

## 解决什么问题

Prometheus / VMAgent 的 pull/scrape 模型无法覆盖：
- 短生命周期任务（批处理 Job、CronJob）
- 防火墙后无法被抓取的服务
- 非 Go/Java/Python 语言环境（无成熟 client library）

metric-gw 提供 HTTP push 入口，应用只需 POST JSON 即可上报指标，网关缓冲后批量转发。

## 架构

```
应用 POST /api/v1/metrics (JSON)
    │
    ▼
metric-gw
  内存队列 ──(满/后端不可达)──▶ 磁盘 WAL 队列
    │                              │
    └──────── 批量 flush worker ───┘
               │           │
         vm_import    remote_write
               │           │
        VictoriaMetrics  Prometheus
```

- 每个后端独立 worker + 独立 per-backend 缓冲队列，互不阻塞
- 正常路径走内存队列，零磁盘开销
- 后端不可达时溢出到磁盘 WAL，恢复后重放
- 磁盘队列满时拒绝写入 (503) 并丢弃最旧数据

## API

### POST /api/v1/metrics

认证: API Key (Header `X-API-Key`) 或 Basic Auth，二选一。

```json
{
  "metrics": [
    {
      "name": "http_requests_total",
      "labels": {"method": "GET", "status": "200"},
      "value": 1234,
      "timestamp": 1720900000
    }
  ]
}
```

- `timestamp` 可选，不传则用网关当前时间
- 单次请求上限: 10MB / 50000 条
- 返回 `202 Accepted`（写入缓冲即返回，不等后端 flush）
- 后端不可达且磁盘队列满时返回 `503`

### 其他端点

- `GET /health` - 存活检查
- `GET /ready` - 就绪检查（至少一个后端可用）
- `GET /metrics` - 自身可观测性指标
- `GET /api/v1/backends/status` - 各后端状态与队列深度

## 配置

```yaml
server:
  listen: ":9201"
  max_body_size: "10MB"

auth:
  mode: apikey          # apikey | basic | none
  api_key: "your-secret-key"
  # basic:
  #   username: admin
  #   password: secret

buffer:
  memory:
    max_items: 100000
  disk:
    enabled: true
    path: "/data/metric-gw"
    max_size: "1GB"

flush:
  batch_size: 5000
  interval: "1s"
  timeout: "10s"
  retry:
    max_attempts: 0
    backoff: exponential
    initial_delay: "1s"
    max_delay: "30s"

backends:
  - name: "vm-single"
    type: vm_import
    url: "http://vmsingle-monitoring:8429/api/v1/import"
  - name: "prometheus"
    type: remote_write
    url: "http://prometheus-server:9090/api/v1/write"
```

## 部署

### Docker

```bash
cd deploy/docker
docker compose up -d
```

### Kubernetes（手动）

```bash
cd deploy
API_KEY="your-secret-key" bash install.sh
```

### CI/CD（自动）

tag push 触发 Gitea Actions 自动构建镜像并部署:

```bash
git tag v1.0.0
git push origin v1.0.0
```

前置条件及配置详见 sre-lab `k8s/gitea/cicd.md`。

Gitea Secrets:
- `API_KEY` - 指标推送认证密钥
- `REGISTRYTOKEN` - Nexus docker registry 密码
- `KUBE_TOKEN` - ci-deployer SA token

## 本地开发

```bash
go build -o bin/metric-gw ./cmd/metric-gw
./bin/metric-gw --config config.yaml
```
