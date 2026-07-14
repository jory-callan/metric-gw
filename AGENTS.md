# metric-gw AGENTS.md

## 项目定位
HTTP 指标推送网关：接收应用 push 的 JSON 指标，经内存+磁盘双缓冲后批量转发到多后端（VictoriaMetrics / Prometheus）。自身不提供查询接口。

## 技术栈
- 语言: Go 1.25
- Web 框架: Echo v4
- 配置: YAML (gopkg.in/yaml.v3，不用 viper，减少依赖)
- 指标暴露: prometheus/client_golang
- remote_write: prometheus/prometheus/prompb (protobuf+snappy)

## 架构
```
HTTP API (JSON) -> 内存队列 -> [满则溢出磁盘 WAL] -> flush worker -> 多后端并行转发
```
- 每个后端独立 worker + 独立 per-backend 缓冲队列
- 缓冲策略: overflow_only=true（仅内存满时落盘），正常路径零磁盘开销
- 磁盘队列: segment 文件顺序读写，无外部依赖

## 目录结构
- `cmd/metric-gw/main.go` - 入口
- `internal/api/` - HTTP handler + 认证中间件 (Echo)
- `internal/config/` - 配置结构与加载校验
- `internal/buffer/` - 内存队列 + 磁盘 segment WAL
- `internal/writer/` - 后端输出 (vm_import / remote_write)
- `internal/flusher/` - 批量 flush worker
- `internal/metrics/` - 自身可观测性指标
- `pkg/model/` - Metric 数据结构（可被外部引用）
- `Dockerfile` - 多阶段构建（不 COPY 配置文件）
- `deploy/` - K8s 部署文件 (扁平 yaml + install.sh / uninstall.sh)
- `deploy/docker/` - Docker Compose 本地部署

## 开发规则
- 所有文件需初始化 Git，每次修改后按 type(scope): subject 格式提交
- 沟通风格要给出专业判断，不要一味迎合，依据独立分析判断
- 永远先确认完整计划，等待用户明确 "start" 信号后再行动
- 用户喜欢简洁清晰的文档
- 每次功能完成后，需要校验确保无报错，整理相关文档需要更新就更新，不需要就直接结束
- 代码要生产可用级别，不要伪代码

## docker 规则
- 所有组件必须走 net-shared 网络，目的是所有组件网络互通

## k8s 规则
- kubectl 和 helm 已配置好可直接使用
- 避免使用 *.local 域名，统一使用 *.czw-sre.internal 域名（内网域名，已在路由器解析）
- 每个组件最少提供 install.sh 和 uninstall.sh
- README 中保留原始远程资源地址说明，只讲怎么用，不列出文件树
- 资源应用命名统一采用类型-名称（如 app-metric-gw）
- k8s 资源限制只设置 limit，不设置 request
- 资源修改执行 install.sh（幂等），不直接 patch/apply
- k8s 内部应用连接走内部 DNS，不走 ingress 域名
- 本地 Docker 仓库: 192.168.5.103:5001 (nexus3 docker-hosted, HTTP)
