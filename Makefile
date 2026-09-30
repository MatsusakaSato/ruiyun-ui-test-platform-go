# ==============================================================================
# 睿云智能工作台 UI 测试平台 Makefile
# ==============================================================================

SHELL := /bin/bash
export GOCACHE ?= /Users/amano/WorkSpace/.gocache

.PHONY: all build build-all run vet fmt lint clean dist cross-compile help

all: build

help:
	@echo "可用命令："
	@echo "  make build          编译 ruiyun 主二进制到 bin/ruiyun"
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
