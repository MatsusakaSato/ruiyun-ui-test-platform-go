package server

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed web/*
var WebFS embed.FS

// DistFS 获取静态文件系统子系统。
//
// 开发模式（RUYIYUN_DEV=1，见 dev.go）下改为读磁盘上的 internal/server/web/，
// 这样改 HTML/CSS/JS 无需重新编译；未开启时走编译期内嵌资源，与生产一致。
func DistFS() (fs.FS, error) {
	if dir, ok := devWebDir(); ok {
		return os.DirFS(dir), nil
	}
	return fs.Sub(WebFS, "web")
}
