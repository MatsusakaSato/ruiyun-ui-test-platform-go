# ==============================================================================
# 睿云智能工作台 UI 测试平台 Makefile
# ==============================================================================

SHELL := /bin/bash
export GOCACHE ?= /Users/amano/WorkSpace/.gocache

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo 1.0.0-dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)

PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build dev dist release

# 编译到 bin/ruiyun（注入 git 版本号，`ruiyun version` 会显示）
build:
	@mkdir -p bin
	@echo "构建中 ($(VERSION), $(COMMIT)) ..."
	@go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ruiyun ./cmd/ruiyun
	@echo "产物: bin/ruiyun"

# 交叉编译 + 打包到 dist/：每个目标平台一个临时目录，产物连同 config.template.yaml
# 一起打成 tar.gz（Windows 为 zip），最后算 SHA-256 校验和。
dist:
	@rm -rf dist
	@mkdir -p dist
	@echo "交叉编译中 ($(VERSION)) ..."
	@for target in $(PLATFORMS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		bin=ruiyun; pkg=dist/tmp_$${os}_$${arch}; \
		archive="dist/ruiyun-$(VERSION)-$${os}-$${arch}"; \
		if [ "$$os" = "windows" ]; then bin=ruiyun.exe; archive="$$archive.zip"; \
		else archive="$$archive.tar.gz"; fi; \
		echo "    编译目标: $$os/$$arch ..."; \
		mkdir -p "$$pkg"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o "$$pkg/$$bin" ./cmd/ruiyun || exit 1; \
		cp config.template.yaml "$$pkg/" 2>/dev/null || true; \
		if [ "$$os" = "windows" ]; then \
			(cd "$$pkg" && zip -q -r "../$${archive#dist/}" .) || exit 1; \
		else \
			(cd "$$pkg" && tar -czf "../$${archive#dist/}" *) || exit 1; \
		fi; \
		rm -rf "$$pkg"; \
	done
	@echo "计算校验和 (SHA-256) ..."
	@cd dist && { shasum -a 256 ruiyun-* 2>/dev/null || sha256sum ruiyun-*; } > checksums.txt
	@echo "打包完成："
	@ls -lh dist

# 用 GitHub Release 发布当前版本，且不附带任何正文说明
release: dist
	@command -v gh >/dev/null 2>&1 || { echo "请先安装 GitHub CLI: https://cli.github.com/"; exit 1; }
	@tag="$(VERSION)"; \
	if ! gh release view "$$tag" >/dev/null 2>&1; then \
		echo "创建 GitHub Release: $$tag（正文为空）..."; \
		gh release create "$$tag" dist/ruiyun-* --title "$$tag" --notes "" --verify-tag; \
	else \
		echo "GitHub Release $$tag 已存在，跳过创建。"; \
	fi

# 开发模式（前台热重载，Ctrl-C 退出）：
#   web/*（HTML/CSS/JS）→ 服务直接读磁盘，免编译，浏览器经 SSE 自动刷新
#   *.go               → 自动重新编译并重启（优先 air，见 .air.toml；没装则用内置监听）
dev: build
	@./bin/ruiyun dev
