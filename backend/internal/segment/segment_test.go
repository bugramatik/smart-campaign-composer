package segment

import (
	"errors"
	"strings"
	"testing"

	"scc/internal/validation"
)

var testCatalog = NewCatalog([]string{"İstanbul", "Ankara", "İzmir"})

func ptr[T any](v T) *T { return &v }

func issuesOf(t *testing.T, err error) []validation.Issue {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("want *validation.Error, got %v", err)
	}
	return ve.Issues
}

func hasIssue(issues []validation.Issue, path, substr string) bool {
	for _, i := range issues {
		if i.Path == path && strings.Contains(i.Message, substr) {
			return true
		}
	}
	return false
}

func TestValidateNormalizesSpellings(t *testing.T) {
	p := Proposal{
		Rule: Rule{Match: MatchAll, Conditions: []Condition{
			{Kind: KindText, Field: "city", Operator: "in", Values: []string{"istanbul", "ISTANBUL", " İzmir "}},
			{Kind: KindEvent, Event: "cart_add", Occurred: ptr(true), WithinDays: ptr(30), Category: ptr("Electronics")},
		}},
		Channels: []string{"SMS", "email", "sms"},
		Summary:  "İstanbul ve İzmir'de son 30 günde sepete elektronik ekleyenler",
	}
	got, err := testCatalog.Validate(p)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v := got.Rule.Conditions[0].Values; len(v) != 2 || v[0] != "İstanbul" || v[1] != "İzmir" {
		t.Errorf("city values = %q, want [İstanbul İzmir]", v)
	}
	if c := got.Rule.Conditions[1].Category; c == nil || *c != "electronics" {
		t.Errorf("category = %v, want electronics", c)
	}
	if ch := got.Channels; len(ch) != 2 || ch[0] != "sms" || ch[1] != "email" {
		t.Errorf("channels = %q, want [sms email]", ch)
	}
}

func TestValidateReportsEveryIssue(t *testing.T) {
	p := Proposal{
		Rule: Rule{Match: "some", Conditions: []Condition{
			{Kind: KindText, Field: "email", Operator: "in", Values: []string{"x"}},
			{Kind: KindText, Field: "city", Operator: "like", Values: []string{"Paris"}},
			{Kind: KindNumber, Field: "age", Operator: "between", Value: ptr(40.0), ValueTo: ptr(30.0)},
			{Kind: KindEvent, Event: "purchase", Occurred: ptr(false), MinCount: ptr(3)},
			{Kind: KindEvent, Event: "signup", Occurred: ptr(true), WithinDays: ptr(9999)},
			{Kind: "sql", Field: "1=1"},
		}},
		Channels: []string{"push"},
	}
	_, err := testCatalog.Validate(p)
	issues := issuesOf(t, err)
	for _, want := range []struct{ path, substr string }{
		{"rule.match", "must be"},
		{"rule.conditions[0].field", "unknown text field"},
		{"rule.conditions[1].operator", "not valid"},
		{"rule.conditions[1].values", `"Paris" is not a known city`},
		{"rule.conditions[2].value_to", "must be >="},
		{"rule.conditions[3].min_count", "occurred=false"},
		{"rule.conditions[4].event", "unknown event"},
		{"rule.conditions[4].within_days", "between 1 and 365"},
		{"rule.conditions[5].kind", "unknown kind"},
		{"channels", "unknown channel"},
		{"summary", "must not be empty"},
	} {
		if !hasIssue(issues, want.path, want.substr) {
			t.Errorf("missing issue %s: %q\n got: %+v", want.path, want.substr, issues)
		}
	}
}

func TestValidateRejectsFieldsFromOtherKinds(t *testing.T) {
	_, err := testCatalog.ValidateRule(Rule{Match: MatchAll, Conditions: []Condition{
		{Kind: KindText, Field: "city", Operator: "in", Values: []string{"Ankara"}, Event: "purchase"},
	}})
	if !hasIssue(issuesOf(t, err), "rule.conditions[0]", "only take") {
		t.Fatal("text condition carrying an event field must be rejected")
	}
}

func TestCompileUsesOnlyBindParameters(t *testing.T) {
	// Compile is the last line of defence: even an unvalidated value must
	// end up as an argument, never in the SQL text.
	evil := "x'); DROP TABLE users; --"
	r := Rule{Match: MatchAny, Conditions: []Condition{
		{Kind: KindText, Field: "city", Operator: "not_in", Values: []string{evil}},
		{Kind: KindNumber, Field: "total_spent", Operator: "between", Value: ptr(100.0), ValueTo: ptr(500.0)},
		{Kind: KindEvent, Event: "purchase", Occurred: ptr(false), WithinDays: ptr(30), Category: ptr("home")},
		{Kind: KindEvent, Event: "page_view", Occurred: ptr(true), MinCount: ptr(5)},
	}}
	got, err := testCatalog.Compile(r, 2)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if strings.Contains(got.Where, "DROP") || strings.Contains(got.Where, "'") {
		t.Fatalf("value leaked into SQL: %s", got.Where)
	}
	want := "(NOT (u.city = ANY($3::text[])) OR " +
		"u.total_spent BETWEEN $4::numeric AND $5::numeric OR " +
		"NOT EXISTS (SELECT 1 FROM events e WHERE e.user_id = u.id AND e.type = $6 AND e.occurred_at >= now() - make_interval(days => $7::int) AND e.category = $8) OR " +
		"(SELECT count(*) FROM events e WHERE e.user_id = u.id AND e.type = $9) >= $10::int)"
	if got.Where != want {
		t.Errorf("Where:\n got %s\nwant %s", got.Where, want)
	}
	if len(got.Args) != 8 {
		t.Fatalf("args = %d, want 8", len(got.Args))
	}
	if vals, ok := got.Args[0].([]string); !ok || vals[0] != evil {
		t.Errorf("args[0] = %#v, want the raw value as a parameter", got.Args[0])
	}
}

func TestCompileFailsClosed(t *testing.T) {
	cases := map[string]Rule{
		"unknown field":    {Match: MatchAll, Conditions: []Condition{{Kind: KindText, Field: "u.id; --", Operator: "in", Values: []string{"1"}}}},
		"unknown operator": {Match: MatchAll, Conditions: []Condition{{Kind: KindNumber, Field: "age", Operator: "<> 0 OR 1", Value: ptr(1.0)}}},
		"unknown event":    {Match: MatchAll, Conditions: []Condition{{Kind: KindEvent, Event: "login", Occurred: ptr(true)}}},
		"bad match":        {Match: "or", Conditions: []Condition{{Kind: KindEvent, Event: "purchase", Occurred: ptr(true)}}},
		"no conditions":    {Match: MatchAll},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := testCatalog.Compile(r, 0); err == nil {
				t.Fatal("Compile accepted an invalid rule")
			}
		})
	}
}
