#!/usr/bin/env bash
# ==============================================================================
# 睿云智能工作台 UI 测试平台（Go 版）构建脚本
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

export GOCACHE="${GOCACHE:-/Users/amano/WorkSpace/.gocache}"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "1.0.0-dev")}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
BUILD_TIME="${BUILD_TIME:-$(date -u +'%Y-%m-%dT%H:%M:%SZ')}"

LDFLAGS="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}"

OUT_DIR="${ROOT_DIR}/bin"
mkdir -p "${OUT_DIR}"

build_ruiyun() {
    echo "==> 构建 ruiyun CLI (${VERSION}, ${COMMIT}) ..."
    go build -trimpath -ldflags "${LDFLAGS}" -o "${OUT_DIR}/ruiyun" ./cmd/ruiyun
    echo "    产物: ${OUT_DIR}/ruiyun"
}

cross_compile() {
    local dist_dir="${ROOT_DIR}/dist"
    rm -rf "${dist_dir}"
    mkdir -p "${dist_dir}"
    echo "==> 交叉编译 ruiyun CLI 到 ${dist_dir} ..."

    local targets=(
        "darwin/arm64"
        "darwin/amd64"
        "linux/amd64"
        "linux/arm64"
        "windows/amd64"
    )

    for target in "${targets[@]}"; do
        local os="${target%/*}"
        local arch="${target#*/}"
        local bin_name="ruiyun"
        local archive_name="ruiyun-${VERSION}-${os}-${arch}"
        if [[ "${os}" == "windows" ]]; then
            bin_name="ruiyun.exe"
        fi
        
        echo "    编译目标: ${os}/${arch} ..."
        local tmp_pkg="${dist_dir}/tmp_${os}_${arch}"
        mkdir -p "${tmp_pkg}"
        
        CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" \
            go build -trimpath -ldflags "${LDFLAGS}" \
            -o "${tmp_pkg}/${bin_name}" ./cmd/ruiyun
            
        cp "${ROOT_DIR}/config.template.yaml" "${tmp_pkg}/" 2>/dev/null || true
        
        cd "${tmp_pkg}"
        if [[ "${os}" == "windows" ]]; then
            zip -q -r "${dist_dir}/${archive_name}.zip" .
        else
            tar -czf "${dist_dir}/${archive_name}.tar.gz" *
        fi
        cd "${ROOT_DIR}"
        rm -rf "${tmp_pkg}"
    done

    echo "==> 计算校验和 (SHA-256) ..."
    cd "${dist_dir}"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 ruiyun-* > checksums.txt
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum ruiyun-* > checksums.txt
    fi
    cd "${ROOT_DIR}"

    echo "==> 交叉编译与打包完成！"
    ls -lh "${dist_dir}"
}

TARGET="${1:-build}"

case "${TARGET}" in
    build|all|build-all)
        build_ruiyun
        ;;
    cross|dist)
        cross_compile
        ;;
    *)
        echo "未知目标: ${TARGET}"
        echo "用法: $0 [build | cross | dist]"
        exit 1
        ;;
esac
