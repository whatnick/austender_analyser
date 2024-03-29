package main

import (
	"html/template"
	"io"

	echo "github.com/labstack/echo/v4"
)

type Template struct {
	templates *template.Template
}

func (t *Template) Add(glob string) {
	t.templates = template.Must(template.ParseGlob(glob))
}

func (t *Template) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}
