// Package content defines campaign message content, its limits and the
// {{variable}} templating used to personalize it.
package content

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"scc/internal/schema"
	"scc/internal/validation"
)

const (
	MaxSMS          = 160
	MaxEmailSubject = 80
	MaxPreheader    = 140
	MaxEmailBody    = 4000
)

// Variables are the personalization fields a template may reference.
var Variables = []string{"first_name", "last_name", "city"}

// Variant is the content for one audience language.
type Variant struct {
	Language       string `json:"language"`
	SMS            string `json:"sms"`
	EmailSubject   string `json:"email_subject"`
	EmailPreheader string `json:"email_preheader"`
	EmailBody      string `json:"email_body"`
}

type Content struct {
	Variants []Variant `json:"variants"`
}

// For returns the variant for lang, falling back to the first variant.
func (c Content) For(lang string) (Variant, bool) {
	for _, v := range c.Variants {
		if v.Language == lang {
			return v, true
		}
	}
	if len(c.Variants) > 0 {
		return c.Variants[0], true
	}
	return Variant{}, false
}

// Spec is what content must satisfy for a given campaign.
type Spec struct {
	Languages []string // every language must have exactly one variant
	Channels  []string // only these channels' fields are checked
	// MaxLengths is the longest value per variable in the audience; templates
	// are measured with these substituted (worst case).
	MaxLengths map[string]int
}

var (
	placeholderRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	strayBraceRe  = regexp.MustCompile(`\{\{|\}\}|\{[a-z_]+\}`)
)

// Validate reports every problem with c against spec.
func Validate(c Content, spec Spec) error {
	var v validation.Collector
	seen := map[string]bool{}
	for i, vr := range c.Variants {
		p := fmt.Sprintf("variants[%d]", i)
		if !slices.Contains(spec.Languages, vr.Language) {
			v.Add(p+".language", "language %q is not needed for this audience (needed: %s)", vr.Language, strings.Join(spec.Languages, ", "))
		}
		if seen[vr.Language] {
			v.Add(p+".language", "duplicate variant for language %q", vr.Language)
		}
		seen[vr.Language] = true

		if slices.Contains(spec.Channels, "sms") {
			checkField(&v, p+".sms", vr.SMS, MaxSMS, spec.MaxLengths)
		}
		if slices.Contains(spec.Channels, "email") {
			checkField(&v, p+".email_subject", vr.EmailSubject, MaxEmailSubject, spec.MaxLengths)
			checkField(&v, p+".email_preheader", vr.EmailPreheader, MaxPreheader, spec.MaxLengths)
			checkField(&v, p+".email_body", vr.EmailBody, MaxEmailBody, spec.MaxLengths)
		}
	}
	for _, lang := range spec.Languages {
		if !seen[lang] {
			v.Add("variants", "missing variant for language %q", lang)
		}
	}
	return v.Err()
}

func checkField(v *validation.Collector, path, tmpl string, limit int, maxLens map[string]int) {
	if strings.TrimSpace(tmpl) == "" {
		v.Add(path, "must not be empty")
		return
	}
	for _, name := range Placeholders(tmpl) {
		if !slices.Contains(Variables, name) {
			v.Add(path, "unknown variable {{%s}} (allowed: %s)", name, varList())
		}
	}
	if stray := strayBraceRe.FindString(placeholderRe.ReplaceAllString(tmpl, "")); stray != "" {
		v.Add(path, "malformed placeholder near %q; use {{variable}}", stray)
	}
	if n := WorstCaseLength(tmpl, maxLens); n > limit {
		v.Add(path, "is %d characters with the longest audience values substituted; limit is %d", n, limit)
	}
}

// Placeholders lists the variable names referenced in tmpl.
func Placeholders(tmpl string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, m[1])
	}
	return out
}

// WorstCaseLength is the character count of tmpl after substituting each
// variable with a value of the longest length seen in the audience.
func WorstCaseLength(tmpl string, maxLens map[string]int) int {
	n := utf8.RuneCountInString(placeholderRe.ReplaceAllString(tmpl, ""))
	for _, name := range Placeholders(tmpl) {
		n += maxLens[name]
	}
	return n
}

// Render substitutes vars into tmpl. Unknown variables render empty; Validate
// prevents them from reaching approval.
func Render(tmpl string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		return vars[placeholderRe.FindStringSubmatch(m)[1]]
	})
}

// Schema is the structured-output JSON schema for Content.
func Schema(languages []string) map[string]any {
	return schema.Object(schema.M{
		"variants": schema.ArrayOf(schema.Object(schema.M{
			"language":        schema.Enum(languages),
			"sms":             schema.String(),
			"email_subject":   schema.String(),
			"email_preheader": schema.String(),
			"email_body":      schema.String(),
		})),
	})
}

func varList() string {
	parts := make([]string, len(Variables))
	for i, v := range Variables {
		parts[i] = "{{" + v + "}}"
	}
	return strings.Join(parts, ", ")
}
