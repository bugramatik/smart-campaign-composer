// Package segment defines the audience rule DSL Claude produces, validates it
// against a whitelist catalog and compiles it to parameterized SQL.
//
// The DSL is deliberately flat (one level of conditions joined by all/any):
// it covers the briefs marketers write, keeps the structured-output schema
// non-recursive, and leaves nothing for the model to express as raw SQL.
package segment

import (
	"slices"
	"strconv"
	"strings"

	"scc/internal/validation"
)

const (
	KindText   = "text"
	KindNumber = "number"
	KindEvent  = "event"

	MatchAll = "all"
	MatchAny = "any"
)

var (
	TextOperators   = []string{"in", "not_in"}
	NumberOperators = []string{"eq", "gt", "gte", "lt", "lte", "between"}
	Channels        = []string{"sms", "email"}
)

type Rule struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
}

// Condition is a tagged union keyed by Kind. Fields that don't belong to the
// kind must be empty; Validate enforces it.
type Condition struct {
	Kind string `json:"kind"`

	// text + number
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	// text
	Values []string `json:"values,omitempty"`
	// number
	Value   *float64 `json:"value,omitempty"`
	ValueTo *float64 `json:"value_to,omitempty"`

	// event
	Event      string  `json:"event,omitempty"`
	Occurred   *bool   `json:"occurred,omitempty"`
	WithinDays *int    `json:"within_days,omitempty"` // nil = any time
	MinCount   *int    `json:"min_count,omitempty"`   // nil = 1
	Category   *string `json:"category,omitempty"`    // nil = any category
}

// Proposal is what Claude returns for a brief: the rule plus the reasoning a
// marketer needs to trust it.
type Proposal struct {
	Rule        Rule     `json:"rule"`
	Channels    []string `json:"channels"`
	Summary     string   `json:"summary"`
	Assumptions []string `json:"assumptions"`
	Unsupported []string `json:"unsupported"`
}

// Validate checks p against the catalog and returns a normalized copy
// (canonical spellings, deduplicated values). All issues are reported at once
// so a single repair round can fix them.
func (c Catalog) Validate(p Proposal) (Proposal, error) {
	var v validation.Collector
	out := p
	out.Rule = c.validateRule(&v, p.Rule, "rule")

	out.Channels = nil
	for _, ch := range p.Channels {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if !contains(Channels, ch) {
			v.Add("channels", "unknown channel %q (allowed: sms, email)", ch)
			continue
		}
		if !contains(out.Channels, ch) {
			out.Channels = append(out.Channels, ch)
		}
	}
	if len(out.Channels) == 0 && len(p.Channels) == 0 {
		v.Add("channels", "at least one channel is required")
	}
	if strings.TrimSpace(p.Summary) == "" {
		v.Add("summary", "summary must not be empty")
	}
	return out, v.Err()
}

// ValidateRule validates a rule on its own (manual edits from the UI).
func (c Catalog) ValidateRule(r Rule) (Rule, error) {
	var v validation.Collector
	out := c.validateRule(&v, r, "rule")
	return out, v.Err()
}

func (c Catalog) validateRule(v *validation.Collector, r Rule, path string) Rule {
	out := Rule{Match: r.Match}
	if r.Match != MatchAll && r.Match != MatchAny {
		v.Add(path+".match", "must be %q or %q", MatchAll, MatchAny)
	}
	if len(r.Conditions) == 0 {
		v.Add(path+".conditions", "at least one condition is required")
	}
	if len(r.Conditions) > maxConditions {
		v.Add(path+".conditions", "at most %d conditions are allowed, got %d", maxConditions, len(r.Conditions))
	}
	for i, cond := range r.Conditions {
		p := path + ".conditions[" + strconv.Itoa(i) + "]"
		switch cond.Kind {
		case KindText:
			out.Conditions = append(out.Conditions, c.validateText(v, cond, p))
		case KindNumber:
			out.Conditions = append(out.Conditions, c.validateNumber(v, cond, p))
		case KindEvent:
			out.Conditions = append(out.Conditions, c.validateEvent(v, cond, p))
		default:
			v.Add(p+".kind", "unknown kind %q (allowed: text, number, event)", cond.Kind)
		}
	}
	return out
}

func (c Catalog) validateText(v *validation.Collector, cond Condition, p string) Condition {
	out := Condition{Kind: KindText, Field: cond.Field, Operator: cond.Operator}
	f, ok := c.field(cond.Field)
	if !ok || f.Type != TypeText {
		v.Add(p+".field", "unknown text field %q (allowed: %s)", cond.Field, strings.Join(c.fieldNames(TypeText), ", "))
		return out
	}
	if !contains(TextOperators, cond.Operator) {
		v.Add(p+".operator", "operator %q is not valid for text fields (allowed: %s)", cond.Operator, strings.Join(TextOperators, ", "))
	}
	if cond.Value != nil || cond.ValueTo != nil || cond.Event != "" {
		v.Add(p, "text conditions only take field, operator and values")
	}
	if len(cond.Values) == 0 {
		v.Add(p+".values", "at least one value is required")
	}
	if len(cond.Values) > maxValues {
		v.Add(p+".values", "at most %d values are allowed", maxValues)
	}
	for _, raw := range cond.Values {
		val, ok := canonical(raw, f.Values)
		if !ok {
			v.Add(p+".values", "%q is not a known %s (allowed: %s)", raw, f.Name, strings.Join(f.Values, ", "))
			continue
		}
		if !contains(out.Values, val) {
			out.Values = append(out.Values, val)
		}
	}
	return out
}

func (c Catalog) validateNumber(v *validation.Collector, cond Condition, p string) Condition {
	out := Condition{Kind: KindNumber, Field: cond.Field, Operator: cond.Operator, Value: cond.Value}
	f, ok := c.field(cond.Field)
	if !ok || f.Type != TypeNumber {
		v.Add(p+".field", "unknown number field %q (allowed: %s)", cond.Field, strings.Join(c.fieldNames(TypeNumber), ", "))
		return out
	}
	if !contains(NumberOperators, cond.Operator) {
		v.Add(p+".operator", "operator %q is not valid for number fields (allowed: %s)", cond.Operator, strings.Join(NumberOperators, ", "))
	}
	if len(cond.Values) > 0 || cond.Event != "" {
		v.Add(p, "number conditions only take field, operator, value and value_to")
	}
	if cond.Value == nil {
		v.Add(p+".value", "value is required")
		return out
	}
	inRange := func(name string, x float64) {
		if x < f.Min || x > f.Max {
			v.Add(p+"."+name, "%s must be between %g and %g, got %g", f.Name, f.Min, f.Max, x)
		}
	}
	inRange("value", *cond.Value)
	if cond.Operator == "between" {
		if cond.ValueTo == nil {
			v.Add(p+".value_to", "value_to is required for between")
			return out
		}
		inRange("value_to", *cond.ValueTo)
		if *cond.ValueTo < *cond.Value {
			v.Add(p+".value_to", "value_to (%g) must be >= value (%g)", *cond.ValueTo, *cond.Value)
		}
		out.ValueTo = cond.ValueTo
	} else if cond.ValueTo != nil {
		v.Add(p+".value_to", "value_to is only allowed with between")
	}
	return out
}

func (c Catalog) validateEvent(v *validation.Collector, cond Condition, p string) Condition {
	out := Condition{Kind: KindEvent, Event: cond.Event, Occurred: cond.Occurred, WithinDays: cond.WithinDays}
	if !contains(c.Events, cond.Event) {
		v.Add(p+".event", "unknown event %q (allowed: %s)", cond.Event, strings.Join(c.Events, ", "))
	}
	if cond.Field != "" || cond.Operator != "" || len(cond.Values) > 0 || cond.Value != nil {
		v.Add(p, "event conditions only take event, occurred, within_days, min_count and category")
	}
	if cond.Occurred == nil {
		v.Add(p+".occurred", "occurred (true/false) is required")
	}
	if d := cond.WithinDays; d != nil && (*d < 1 || *d > c.MaxDays) {
		v.Add(p+".within_days", "within_days must be between 1 and %d, got %d", c.MaxDays, *d)
	}
	if n := cond.MinCount; n != nil {
		switch {
		case *n < 1 || *n > maxMinCount:
			v.Add(p+".min_count", "min_count must be between 1 and %d, got %d", maxMinCount, *n)
		case *n > 1 && cond.Occurred != nil && !*cond.Occurred:
			v.Add(p+".min_count", "min_count > 1 cannot be combined with occurred=false")
		case *n > 1:
			out.MinCount = n
		}
	}
	if cond.Category != nil {
		cat, ok := canonical(*cond.Category, c.Categories)
		if !ok {
			v.Add(p+".category", "unknown category %q (allowed: %s)", *cond.Category, strings.Join(c.Categories, ", "))
		} else {
			out.Category = &cat
		}
	}
	return out
}

func (c Catalog) fieldNames(t FieldType) []string {
	var names []string
	for _, f := range c.Fields {
		if f.Type == t {
			names = append(names, f.Name)
		}
	}
	return slices.Clip(names)
}
