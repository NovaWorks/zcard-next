# docker build -f deploy/Dockerfile --build-arg VERSION=vX.Y.Z -t zcard:vX.Y.Z .
FROM node:22-alpine AS version
ARG VERSION=auto
WORKDIR /version
COPY server/CHANGELOG.json ./CHANGELOG.json
RUN node -e 'const fs=require("fs"); const v=JSON.parse(fs.readFileSync("CHANGELOG.json"))[0]?.version; if(!/^v[0-9]+\.[0-9]+\.[0-9]+$/.test(v)) throw Error("Invalid source release version"); const requested=process.env.VERSION; if(requested && !["auto","dev",v].includes(requested)) throw Error("VERSION must match source CHANGELOG: " + v); fs.writeFileSync("version",v)'

FROM node:22-alpine AS frontend
WORKDIR /fe
RUN corepack enable
COPY storefront/package.json storefront/pnpm-lock.yaml storefront/pnpm-workspace.yaml ./storefront/
RUN cd storefront && pnpm install --frozen-lockfile
COPY packages/ ./packages/
COPY storefront/ ./storefront/
RUN cd storefront && pnpm build
# 工作区包必须随依赖安装一起提供；.env.prod 提供公开构建默认值。
COPY admin/ ./admin/
RUN cd admin && pnpm install --frozen-lockfile && pnpm build

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=frontend /fe/storefront/dist ./web/storefront
COPY --from=frontend /fe/admin/dist ./web/admin
COPY --from=version /version/version /build-version
RUN VERSION="$(cat /build-version)" && CGO_ENABLED=0 go build -trimpath -tags fullstack \
    -ldflags "-s -w -X main.Version=${VERSION}" -o /out/zcard ./cmd/zcard
RUN mkdir -p /runtime/configs /runtime/data /runtime/backups

# 二进制随镜像更新；全部运行文件（含安装向导生成的配置）写入 /app 卷。
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/zcard /zcard
COPY --from=builder --chown=65532:65532 /runtime/ /app/
WORKDIR /app
ENV ZCARD_CONTAINER=1
VOLUME ["/app"]
EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/zcard"]
CMD ["serve", "-conf", "/app/configs"]
