package server

import (
	"embed"
	"io/fs"
)

//go:embed web/*
var WebFS embed.FS

// DistFS 获取静态文件系统子系统
func DistFS() (fs.FS, error) {
	return fs.Sub(WebFS, "web")
}
