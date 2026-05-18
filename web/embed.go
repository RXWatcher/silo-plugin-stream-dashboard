package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

func FSEmbed() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("web: " + err.Error())
	}
	return sub
}
