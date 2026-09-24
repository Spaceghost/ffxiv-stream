// Package assets holds the files ffxiv-stream installs, as templates rendered
// from the configuration. They are the setup this project grew out of, kept
// with the comments that explain each piece.
package assets

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed files
var files embed.FS

var tmpl = template.Must(template.New("").ParseFS(files, "files/*.tmpl"))

// Render renders a template by name (a file name, or a {{define}} in units.tmpl).
func Render(name string, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return bytes.TrimLeft(buf.Bytes(), "\n"), nil
}

// MustRender is Render for templates the tests cover.
func MustRender(name string, data any) []byte {
	out, err := Render(name, data)
	if err != nil {
		panic(err)
	}
	return out
}

// Raw returns a file shipped as is (the dmabuf-wait shim's C source).
func Raw(name string) []byte {
	data, err := files.ReadFile("files/" + name)
	if err != nil {
		panic(err)
	}
	return data
}
