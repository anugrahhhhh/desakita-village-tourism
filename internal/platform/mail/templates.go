package mail

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"
)

// Templates renders plain-text emails from emails/<lang>/<name>.txt. Each file defines a
// "subject" and a "body" template. Adding a language means adding a directory.
type Templates struct {
	set map[string]*template.Template
}

// LoadTemplates parses every template for lang.
func LoadTemplates(fsys fs.FS, lang string) (*Templates, error) {
	files, err := fs.Glob(fsys, "emails/"+lang+"/*.txt")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("mail: no email templates for %q", lang)
	}
	t := &Templates{set: map[string]*template.Template{}}
	for _, f := range files {
		tmpl, err := template.New("").Option("missingkey=error").ParseFS(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("mail: parse %s: %w", f, err)
		}
		for _, part := range []string{"subject", "body"} {
			if tmpl.Lookup(part) == nil {
				return nil, fmt.Errorf("mail: %s has no %q template", f, part)
			}
		}
		t.set[strings.TrimSuffix(path.Base(f), ".txt")] = tmpl
	}
	return t, nil
}

// Names lists the loaded templates.
func (t *Templates) Names() []string {
	out := make([]string, 0, len(t.set))
	for n := range t.set {
		out = append(out, n)
	}
	return out
}

// Render executes template name with data and returns the subject and body.
func (t *Templates) Render(name string, data any) (subject, body string, err error) {
	tmpl, ok := t.set[name]
	if !ok {
		return "", "", fmt.Errorf("mail: unknown template %q", name)
	}
	var s, b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&s, "subject", data); err != nil {
		return "", "", fmt.Errorf("mail: render %s subject: %w", name, err)
	}
	if err := tmpl.ExecuteTemplate(&b, "body", data); err != nil {
		return "", "", fmt.Errorf("mail: render %s body: %w", name, err)
	}
	subject = strings.Join(strings.Fields(s.String()), " ")
	return subject, strings.TrimSpace(b.String()) + "\n", nil
}
