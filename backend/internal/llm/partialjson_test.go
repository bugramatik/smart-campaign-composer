package llm

import (
	"encoding/json"
	"testing"
)

func TestCompleteJSON(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{``, ``, false},
		{`{`, `{}`, true},
		{`{"variants": [{"language": "tr", "sms": "Merhaba Ay`, `{"variants": [{"language": "tr", "sms": "Merhaba Ay"}]}`, true},
		{`{"variants": [{"language": "tr", "sm`, `{"variants": [{"language": "tr"}]}`, true},
		{`{"a": 12`, `{}`, true},
		{`{"a": 12,`, `{"a": 12}`, true},
		{`{"a": "x\`, `{"a": "x"}`, true},
		{`{"a": "\u00`, `{"a": ""}`, true},
		{`{"a": [1, 2, {"b": tr`, `{"a": [1, 2, {}]}`, true},
		{`{"a": "}{]["`, `{"a": "}{]["}`, true},
		{`{"done": true}`, `{"done": true}`, true},
		{`]`, ``, false},
	}
	for _, c := range cases {
		got, ok := CompleteJSON(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("CompleteJSON(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCompleteJSONEveryPrefixIsValid(t *testing.T) {
	doc := `{"variants":[{"language":"tr","sms":"Merhaba {{first_name}}, \"özel\" %20 indirim!\n","email_subject":"Sepetin"}],"n":-1.5e3,"ok":false,"x":null}`
	for i := 1; i <= len(doc); i++ {
		if out, ok := CompleteJSON(doc[:i]); ok && !jsonValid(out) {
			t.Fatalf("prefix %d produced invalid JSON %q", i, out)
		}
	}
}

func jsonValid(s string) bool { return json.Valid([]byte(s)) }
