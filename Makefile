# ==============================================================================
# 睿云智能工作台 UI 测试平台 Makefile
# ==============================================================================

SHELL := /bin/bash
export GOCACHE ?= /Users/amano/WorkSpace/.gocache

.PHONY: all build build-all run dev dev-sh vet fmt lint clean dist cross-compile help

all: build

help:
	@echo "可用命令："
	@echo "  make build          编译 ruiyun 主二进制到 bin/ruiyun"
	@echo "  make dev            开发模式（air 热重载：Go 改动自动重启；web/* 免编译热更新）"
	@echo "  make dev-sh         同上，但不依赖 air（纯 shell 版）"
	@echo "  make run            编译并启动平台服务"
	@echo "  make vet            运行 go vet 静态检查"
	@echo "  make fmt            格式化所有 Go 源码 (gofmt -w)"
	@echo "  make lint           源码风格与 vet 综合检查"
	@echo "  make cross-compile  多平台交叉编译 (macOS, Linux, Windows) 到 dist/"
	@echo "  make clean          清理编译产物与临时文件"

build:
	@./scripts/build.sh build

build-all: build

run: build
	@./bin/ruiyun serve

# 开发模式：优先用 air（Go 生态的热重载工具）重启后端；
# 前端 html/css/js 由 RUYIYUN_DEV=1 直接从磁盘读取 + 浏览器自动刷新，无需重新编译。
# 机器没装 air 时自动退回内置脚本 scripts/dev.sh（效果相同，零依赖）。
# 说明：air 只负责「改 Go 自动重编译重启」，浏览器刷新与免编译读前端由项目开发模式提供。
dev:
	@if command -v air >/dev/null 2>&1; then \
		echo "[dev] 使用 air 启动（监听 *.go 自动重启；web/* 走热更新免编译）"; \
		RUYIYUN_DEV=1 air; \
	else \
		echo "[dev] 未找到 air，改用内置脚本（安装：go install github.com/air-verse/air@latest）"; \
		./scripts/dev.sh; \
	fi

# 不依赖任何外部工具的开发模式（纯 shell 版，与 make dev 等价）
dev-sh:
	@./scripts/dev.sh

vet:
	@go vet ./...

fmt:
	@gofmt -w internal cmd

lint: vet
	@test -z "$$(gofmt -l internal cmd)" || (echo "以下文件未格式化，请运行 make fmt:" && gofmt -l internal cmd && exit 1)
	@echo "代码规范与静态检查全部通过！"

cross-compile: dist

dist:
	@./scripts/build.sh dist

clean:
	@rm -rf bin dist coverage.out coverage.html
	@echo "清理完成。"
