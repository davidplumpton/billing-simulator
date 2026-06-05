package app

import (
	"embed"
	"fmt"
	"html/template"
)

// appTemplateFS contains reusable HTML templates embedded into the single Go binary.
//
//go:embed templates/*.html
var appTemplateFS embed.FS

// mustEmbeddedTemplate parses embedded templates and returns the named root template.
func mustEmbeddedTemplate(name string, filenames ...string) *template.Template {
	templates := mustEmbeddedTemplateSet(name, filenames...)
	tmpl := templates.Lookup(name)
	if tmpl == nil {
		panic(fmt.Sprintf("embedded template %q was not defined", name))
	}
	return tmpl
}

// mustEmbeddedTemplateSet parses embedded template files into one associated template set.
func mustEmbeddedTemplateSet(name string, filenames ...string) *template.Template {
	paths := make([]string, 0, len(filenames))
	for _, filename := range filenames {
		paths = append(paths, "templates/"+filename)
	}
	templates, err := template.New(name).ParseFS(appTemplateFS, paths...)
	if err != nil {
		panic(fmt.Sprintf("parse embedded template %q: %v", name, err))
	}
	return templates
}
