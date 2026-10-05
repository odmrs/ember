// Package web holds the dashboard assets embedded into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static is the dashboard file tree rooted at the static directory.
var Static = mustSub(embedded, "static")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
