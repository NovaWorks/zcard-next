# 多阶段构建（规划 §10.1 完整形态）：前端 dist → go:embed fullstack 单二进制
# → distroless 运行镜像。dist 缺失即编译失败（友商纪律）。

# ---------- 前端阶段 ----------
FROM node:22-alpine AS frontend
WORKDIR /fe
RUN corepack enable
# storefront
COPY storefront/package.json storefront/pnpm-lock.yaml storefront/pnpm-workspace.yaml ./storefront/
RUN cd storefront && pnpm install --frozen-lockfile
COPY storefront/ ./storefront/
RUN cd storefront && pnpm build
# admin（VITE_BASE_URL=/admin/ 随 .env 提交；产物资产引用带前缀）
COPY admin/package.json admin/pnpm-lock.yaml admin/pnpm-workspace.yaml ./admin/
RUN cd admin && pnpm install --frozen-lockfile
COPY admin/ ./admin/
RUN cd admin && pnpm build

# ---------- 构建阶段 ----------
FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /src
# 前端产物 → web/ embed 锚点（make web-dist 的容器内等价物）
COPY --from=frontend /fe/storefront/dist ./web/storefront
COPY --from=frontend /fe/admin/dist ./web/admin
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -tags fullstack \
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
