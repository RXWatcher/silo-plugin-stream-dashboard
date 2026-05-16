package web

import "embed"

//go:embed dist
var dist embed.FS

func FSEmbed() embed.FS { return dist }
