package demo

import (
	"encoding/json"
	"testing"

	"scc/internal/segment"
)

// The demo stands in for Claude in a keyless setup, so its output must pass
// the same validation as the real model's for the example briefs in the UI.
func TestProposeSegmentExampleBriefs(t *testing.T) {
	cat := segment.NewCatalog([]string{"İstanbul", "Ankara", "İzmir"})
	catJSON, _ := json.Marshal(cat)
	tests := []struct {
		brief string
		want  []string // kind/field/event of each condition, in order
	}{
		{"Son 30 günde sepete ürün ekleyip satın almayan İstanbullu kullanıcılara %15 indirim kodu gönder.", []string{"city", "cart_add", "purchase"}},
		{"Altın ve platin üyelerimize yeni sezon moda koleksiyonunu duyur, 48 saat erken erişim hakkı ver.", []string{"loyalty_tier"}},
		{"Son 90 günde hiç alışveriş yapmayan ama elektronik ürünleri inceleyen kullanıcıları geri kazan; kargo bedava.", []string{"product_view", "purchase"}},
		{"İzmir ve Ankara'daki 25–40 yaş arası kullanıcılara hafta sonu spor ürünlerinde 2 al 1 öde kampanyası.", []string{"city", "age"}},
	}
	for _, tt := range tests {
		p := proposeSegment(tt.brief, string(catJSON))
		var got []string
		for _, c := range p.Rule.Conditions {
			if c.Kind == segment.KindEvent {
				got = append(got, c.Event)
			} else {
				got = append(got, c.Field)
			}
		}
		if len(got) != len(tt.want) {
			t.Fatalf("%q: conditions %v, want %v", tt.brief, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("%q: conditions %v, want %v", tt.brief, got, tt.want)
			}
		}
		if _, err := cat.ValidateRule(p.Rule); err != nil {
			t.Errorf("%q: rule does not validate: %v", tt.brief, err)
		}
	}
}

func TestProposeSegmentDetails(t *testing.T) {
	p := proposeSegment("Altın ve platin üyelerimize yeni sezon", `{"fields":[]}`)
	if v := p.Rule.Conditions[0].Values; len(v) != 2 || v[0] != "platinum" || v[1] != "gold" {
		t.Errorf("tiers = %v", v)
	}
	p = proposeSegment("son 90 günde elektronik ürünleri inceleyen", `{"fields":[]}`)
	if c := p.Rule.Conditions[0]; c.Category == nil || *c.Category != "electronics" || *c.WithinDays != 90 {
		t.Errorf("view condition = %+v", c)
	}
}
