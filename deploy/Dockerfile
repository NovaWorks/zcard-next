# 多阶段构建：前端构建 → go build（M2 交付完整链路，M0 先提供后端镜像）
# 最终目标形态（规划 §10.1）：-tags fullstack 以 go:embed 嵌入 admin+storefront dist，
# dist 缺失即编译失败（友商纪律）。当前阶段仅构建纯后端二进制。

# ---------- 构建阶段 ----------
FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /src
# 先拷贝依赖清单，利用层缓存
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.Version=${VERSION}" \
    -o /out/zcard ./cmd/zcard

# ---------- 运行阶段（distroless，非 root）----------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/zcard /zcard
# 数据目录：SQLite 文件 / 本地上传
VOLUME ["/data"]
ENV ZCARD_DATA_DIR=/data
EXPOSE 8000 9000
USER nonroot:nonroot
ENTRYPOINT ["/zcard"]
CMD ["serve"]
