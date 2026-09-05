GOHOSTOS:=$(shell go env GOHOSTOS)
GOPATH:=$(shell go env GOPATH)
VERSION?=$(shell git describe --tags --always 2>/dev/null || echo dev)

# 三方言迁移线（ADR-D18：每方言独立版本线）
DEV_MYSQL_PORT?=13306
DEV_PG_PORT?=15432

.PHONY: init
# 安装开发工具（wire/buf/atlas；ent 经 go generate 内联运行无需安装）
init:
	go install github.com/google/wire/cmd/wire@latest
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install ariga.io/atlas/cmd/atlas@latest 2>/dev/null || echo "atlas CLI 可选（make migrate-diff 走 go run，不强依赖）"

.PHONY: api
# 生成 api/* 对外 API（pb + http + grpc + openapi）
api:
	buf generate --template buf.gen.yaml

.PHONY: config
# 生成 internal/conf 配置代码
config:
	buf generate --template buf.gen.config.yaml

.PHONY: ent
# 生成 Ent 代码（schema 唯一真理源 → ent/）
ent:
	go generate ./internal/data/ent/

.PHONY: wire
# 生成依赖装配（cmd/zcard/wire_gen.go）
wire:
	cd cmd/zcard && wire

.PHONY: generate
# 全量生成（生成物提交仓库；CI 校验无 diff）
generate: api config ent wire
	go mod tidy

.PHONY: check-gen
# 校验生成物与提交内容一致（离线可构建 + review 可见 schema 变更）
check-gen: generate
	git diff --exit-code -- . ':(exclude)go.sum' || (echo "ERROR: 生成物存在未提交变更，请运行 make generate 后提交"; exit 1)

.PHONY: build
# 构建单二进制（API-only；前端经外部静态服务部署）
build:
	mkdir -p bin/ && go build -ldflags "-X main.Version=$(VERSION)" -o ./bin/zcard ./cmd/zcard

.PHONY: web-dist
# 前端产物 → web/（embed 锚点；go:embed 无法引用模块外路径）。
# monorepo 两前端构建后拷贝；重复执行幂等（rsync --delete 保持与源一致）。
web-dist:
	cd ../storefront && pnpm install --frozen-lockfile && pnpm build
	cd ../admin && pnpm install --frozen-lockfile && pnpm build
	rm -rf web/storefront web/admin
	mkdir -p web/storefront web/admin
	cp -R ../storefront/dist/ web/storefront/
	cp -R ../admin/dist/ web/admin/

.PHONY: build-fullstack
# 完整交付形态（）：单二进制嵌入 admin+storefront（dist 缺失即编译失败）。
# 首次/前端有变更时自动重建产物；SKIP_WEB_DIST=1 跳过（产物已就绪时加速）。
build-fullstack:
ifndef SKIP_WEB_DIST
	make web-dist
endif
	mkdir -p bin/ && go build -tags fullstack -ldflags "-X main.Version=$(VERSION)" -o ./bin/zcard ./cmd/zcard

.PHONY: run
# 本地运行（SQLite 起步；默认读取 configs/config.yaml）
run: build
	./bin/zcard serve -conf configs

.PHONY: test
# 全量测试（单元 + 架构守护；SQLite 线。MySQL/PG 集成线 起进 CI）
test:
	go test ./...

.PHONY: test-integration
# 集成线（）：真 MySQL/PG。-tags=integration + DSN 环境变量
# （ZCARD_TEST_MYSQL_DSN / ZCARD_TEST_PG_DSN），未配置的方言自动 Skip。
# 本地起库：docker run -d -e MYSQL_ROOT_PASSWORD=root -p 3306:3306 mysql:8
# docker run -d -e POSTGRES_PASSWORD=pass -p 5432:5432 postgres:16
test-integration:
	go test -tags=integration -count=1 -timeout 15m ./internal/testint/...

.PHONY: lint
# 静态检查（gofmt：生成物豁免；CI 起接 golangci-lint）
lint:
	@out=$$(gofmt -l . | grep -v -e '^internal/data/ent/' -e '\.pb\.go$$' -e 'wire_gen.go' || true); \
	if [ -n "$$out" ]; then echo "存在未格式化文件:"; echo "$$out"; exit 1; fi
	go vet ./...

.PHONY: clean
clean:
	rm -rf bin/ data/ dist/

# ---------------- 在线更新发行（） ----------------

NOTES?=CHANGELOG.md

.PHONY: release
# 发行一套在线更新产物到 dist/：双架构裸二进制 + SHA256SUMS + 签名 update.json。
# 前置：HEAD 必须已打 vX.Y.Z tag（防版本号落后）；私钥 .zcard-update-key（或 ZCARD_SIGN_KEY）。
# 产物上传 GitHub Release（tag 对应，全部文件平铺）或自建静态源目录。
release:
	@tag=$$(git describe --tags --exact-match 2>/dev/null) || { echo "ERROR: HEAD 未打 vX.Y.Z tag——先提交并 git tag，再发行"; exit 1; }; \
	case "$$tag" in v[0-9]*.[0-9]*.[0-9]) ;; *) echo "ERROR: tag 须为纯 vX.Y.Z（sign 拒绝预发布后缀），得到 $$tag"; exit 1;; esac; \
	echo "==> release $$tag"; \
	rm -rf dist && mkdir -p dist && \
	make web-dist && \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags fullstack -trimpath -ldflags "-X main.Version=$$tag" -o dist/zcard-linux-amd64 ./cmd/zcard && \
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags fullstack -trimpath -ldflags "-X main.Version=$$tag" -o dist/zcard-linux-arm64 ./cmd/zcard && \
	(cd dist && shasum -a 256 zcard-linux-amd64 zcard-linux-arm64 > SHA256SUMS) && \
	notes=""; [ -f "$(NOTES)" ] && notes="--notes-file $(NOTES)"; \
	hist=""; [ -f "CHANGELOG.json" ] && hist="--history-file CHANGELOG.json"; \
	go run ./cmd/zcard self-update sign --key .zcard-update-key --dir dist --version $$tag $$notes $$hist && \
	echo "==> 完成 dist/：zcard-linux-amd64 / zcard-linux-arm64 / SHA256SUMS / update.json" && \
	echo "    上传 GitHub Release（tag=$$tag，四个文件平铺）或静态源目录；加速镜像与后台 auto 源随即可用"

# ---------------- 迁移（Atlas 版本化，禁止手写 DDL） ----------------

.PHONY: migrate-diff-sqlite
# 生成 SQLite 迁移（无需 docker：内存 dev-db）
migrate-diff-sqlite:
	go run ./tools/migrate-diff -dialect sqlite -name "$(NAME)"

.PHONY: migrate-diff-mysql
# 生成 MySQL 迁移（docker dev-db）
migrate-diff-mysql:
	@docker run -d --rm --name zcard-dev-mysql -p $(DEV_MYSQL_PORT):3306 \
		-e MYSQL_DATABASE=dev -e MYSQL_ROOT_PASSWORD=pass mysql:8 >/dev/null || true
	@until docker exec zcard-dev-mysql mysqladmin ping -ppass --silent 2>/dev/null; do sleep 1; done
	go run ./tools/migrate-diff -dialect mysql -url "mysql://root:pass@localhost:$(DEV_MYSQL_PORT)/dev" -name "$(NAME)"
	@docker stop zcard-dev-mysql >/dev/null || true

.PHONY: migrate-diff-pg
# 生成 PostgreSQL 迁移（docker dev-db）
migrate-diff-pg:
	@docker run -d --rm --name zcard-dev-pg -p $(DEV_PG_PORT):5432 \
		-e POSTGRES_DB=dev -e POSTGRES_PASSWORD=pass postgres:16 >/dev/null || true
	@until docker exec zcard-dev-pg pg_isready -U postgres --quiet 2>/dev/null; do sleep 1; done
	go run ./tools/migrate-diff -dialect postgres -url "postgres://postgres:pass@localhost:$(DEV_PG_PORT)/dev?sslmode=disable" -name "$(NAME)"
	@docker stop zcard-dev-pg >/dev/null || true

.PHONY: migrate-diff
# 生成指定方言迁移：make migrate-diff DIALECT=mysql NAME=add_xxx
migrate-diff:
ifeq ($(DIALECT),sqlite)
	@$(MAKE) migrate-diff-sqlite NAME="$(NAME)"
else ifeq ($(DIALECT),mysql)
	@$(MAKE) migrate-diff-mysql NAME="$(NAME)"
else ifeq ($(DIALECT),postgres)
	@$(MAKE) migrate-diff-pg NAME="$(NAME)"
else
	@echo "用法: make migrate-diff DIALECT=sqlite|mysql|postgres NAME=变更名"; exit 2
endif

.PHONY: migrate-apply
# 对当前 config 指向的库执行迁移（等价启动期行为）
migrate-apply: build
	./bin/zcard migrate -conf configs

# show help
help:
	@echo ''
	@echo 'Usage:'
	@echo ' make [target]'
	@echo ''
	@echo 'Targets:'
	@awk '/^[a-zA-Z\-\_0-9]+:/ { \
	helpMessage = match(lastLine, /^# (.*)/); \
		if (helpMessage) { \
			helpCommand = substr($$1, 0, index($$1, ":")); \
			helpMessage = substr(lastLine, RSTART + 2, RLENGTH); \
			printf "\033[36m%-22s\033[0m %s\n", helpCommand,helpMessage; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

.PHONY: git-hooks
# 启用仓库管理的 git 钩子（新机器克隆后执行一次）
# pre-push：开源红线——禁止 local/* 与含 saas 的分支推送远端（ ）
git-hooks:
	git config core.hooksPath ../.githooks
	@echo "git 钩子已启用（core.hooksPath=.githooks）"
