# ── 构建阶段 ──────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# 利用 Docker 缓存: 先拷贝依赖文件
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# 拷贝源码
COPY . .

# 静态编译
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /metric-gw ./cmd/metric-gw

# ── 运行阶段 ──────────────────────────────────────────────
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /metric-gw /usr/local/bin/metric-gw

# 数据目录 (磁盘 WAL 队列)
RUN mkdir -p /data/metric-gw

EXPOSE 9201

ENTRYPOINT ["metric-gw"]
CMD ["--config", "/etc/metric-gw/config.yaml"]
