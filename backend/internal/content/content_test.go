package content

import (
	"errors"
	"strings"
	"testing"

	"scc/internal/validation"
)

func valid(lang string) Variant {
	return Variant{
		Language:       lang,
		SMS:            "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor: %10 indirim bugün!",
		EmailSubject:   "{{first_name}}, sepetin seni bekliyor",
		EmailPreheader: "Bugüne özel %10 indirim",
		EmailBody:      "Merhaba {{first_name}},\n\n{{city}} için ücretsiz kargo.",
	}
}

var spec = Spec{Languages: []string{"tr", "en"}, Channels: []string{"sms", "email"}, MaxLengths: map[string]int{"first_name": 10, "city": 9}}

func issues(t *testing.T, err error) []validation.Issue {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("want validation error, got %v", err)
	}
	return ve.Issues
}

func TestValidateAcceptsGoodContent(t *testing.T) {
	if err := Validate(Content{Variants: []Variant{valid("tr"), valid("en")}}, spec); err != nil {
		t.Fatal(err)
	}
}

func TestSMSLimitUsesLongestAudienceValue(t *testing.T) {
	v := valid("tr")
	v.SMS = "{{first_name}} " + strings.Repeat("a", 150) // 151 + name
	c := Content{Variants: []Variant{v, valid("en")}}

	short := spec
	short.MaxLengths = map[string]int{"first_name": 5}
	if err := Validate(c, short); err != nil {
		t.Fatalf("156 chars worst case should pass: %v", err)
	}
	long := spec
	long.MaxLengths = map[string]int{"first_name": 12}
	got := issues(t, Validate(c, long))
	if len(got) != 1 || got[0].Path != "variants[0].sms" || !strings.Contains(got[0].Message, "163 characters") {
		t.Fatalf("issues = %+v", got)
	}
}

func TestSubjectLimitIsInRunesNotBytes(t *testing.T) {
	v := valid("tr")
	v.EmailSubject = strings.Repeat("ş", 80) // 160 bytes, 80 characters
	if err := Validate(Content{Variants: []Variant{v, valid("en")}}, spec); err != nil {
		t.Fatalf("80 Turkish characters must fit an 80-character subject: %v", err)
	}
	v.EmailSubject += "!"
	if err := Validate(Content{Variants: []Variant{v, valid("en")}}, spec); err == nil {
		t.Fatal("81 characters accepted")
	}
}

func TestValidateRejectsBadTemplates(t *testing.T) {
	v := valid("tr")
	v.SMS = "Kodun: {{coupon_code}}"
	v.EmailSubject = "Merhaba {first_name}"
	v.EmailBody = "  "
	got := issues(t, Validate(Content{Variants: []Variant{v, valid("tr")}}, spec))
	want := []string{"unknown variable {{coupon_code}}", "malformed placeholder", "must not be empty", `duplicate variant for language "tr"`, `missing variant for language "en"`}
	for _, w := range want {
		found := false
		for _, i := range got {
			found = found || strings.Contains(i.Message, w)
		}
		if !found {
			t.Errorf("missing issue %q in %+v", w, got)
		}
	}
}

func TestOnlySelectedChannelsAreChecked(t *testing.T) {
	v := valid("tr")
	v.EmailSubject = ""
	smsOnly := Spec{Languages: []string{"tr"}, Channels: []string{"sms"}}
	if err := Validate(Content{Variants: []Variant{v}}, smsOnly); err != nil {
		t.Fatalf("email fields must not matter for an SMS-only send: %v", err)
	}
}

func TestRender(t *testing.T) {
	got := Render("Merhaba {{ first_name }} ({{city}}){{unknown}}", map[string]string{"first_name": "Ayşe", "city": "İzmir"})
	if want := "Merhaba Ayşe (İzmir)"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestForFallsBackToFirstVariant(t *testing.T) {
	c := Content{Variants: []Variant{valid("tr"), valid("en")}}
	if v, ok := c.For("en"); !ok || v.Language != "en" {
		t.Fatalf("For(en) = %v %v", v.Language, ok)
	}
	if v, _ := c.For("de"); v.Language != "tr" {
		t.Fatalf("For(de) fell back to %q, want tr", v.Language)
	}
}

func TestAnalyzeSMS(t *testing.T) {
	cases := []struct {
		text     string
		encoding string
		segments int
	}{
		{"Hello, 20% off today!", "GSM-7", 1},
		{strings.Repeat("a", 160), "GSM-7", 1},
		{strings.Repeat("a", 161), "GSM-7", 2},
		{"Sepetin seni bekliyor, şimdi al", "UCS-2", 1},
		{strings.Repeat("ı", 71), "UCS-2", 2},
	}
	for _, c := range cases {
		got := AnalyzeSMS(c.text)
		if got.Encoding != c.encoding || got.Segments != c.segments {
			t.Errorf("AnalyzeSMS(%.20q) = %+v, want %s/%d", c.text, got, c.encoding, c.segments)
		}
	}
}
