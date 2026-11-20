package segment

import (
	"strings"
)

type FieldType string

const (
	TypeText   FieldType = "text"
	TypeNumber FieldType = "number"
)

// Field is one user attribute the segment DSL may filter on. expr is the SQL
// expression the compiler emits for it: a code-owned constant, never input.
type Field struct {
	Name   string    `json:"name"`
	Label  string    `json:"label"`
	Type   FieldType `json:"type"`
	Values []string  `json:"values,omitempty"` // closed set for text fields; nil = any value
	Min    float64   `json:"min,omitempty"`
	Max    float64   `json:"max,omitempty"`
	expr   string
}

// Catalog is the whitelist the validator and compiler work against.
type Catalog struct {
	Fields     []Field  `json:"fields"`
	Events     []string `json:"events"`
	Categories []string `json:"categories"`
	MaxDays    int      `json:"max_days"`
}

var (
	EventTypes = []string{"page_view", "product_view", "cart_add", "purchase"}
	Categories = []string{"electronics", "fashion", "home", "beauty", "sports"}
)

const (
	maxConditions = 10
	maxValues     = 30
	maxDays       = 365
	maxMinCount   = 100
)

// NewCatalog builds the catalog. cities comes from the users table so the
// model can only target cities that exist.
func NewCatalog(cities []string) Catalog {
	return Catalog{
		Fields: []Field{
			{Name: "city", Label: "Şehir", Type: TypeText, Values: cities, expr: "u.city"},
			{Name: "language", Label: "Dil", Type: TypeText, Values: []string{"tr", "en"}, expr: "u.language"},
			{Name: "loyalty_tier", Label: "Sadakat seviyesi", Type: TypeText, Values: []string{"bronze", "silver", "gold", "platinum"}, expr: "u.loyalty_tier"},
			{Name: "age", Label: "Yaş", Type: TypeNumber, Min: 0, Max: 120, expr: "date_part('year', age(u.birth_date))"},
			{Name: "total_orders", Label: "Toplam sipariş", Type: TypeNumber, Min: 0, Max: 100000, expr: "u.total_orders"},
			{Name: "total_spent", Label: "Toplam harcama (TL)", Type: TypeNumber, Min: 0, Max: 100000000, expr: "u.total_spent"},
			{Name: "days_since_signup", Label: "Üyelik süresi (gün)", Type: TypeNumber, Min: 0, Max: 36500, expr: "(current_date - u.created_at::date)"},
		},
		Events:     EventTypes,
		Categories: Categories,
		MaxDays:    maxDays,
	}
}

func (c Catalog) field(name string) (Field, bool) {
	for _, f := range c.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// canonical matches v against a closed set ignoring case and Turkish
// diacritics ("istanbul", "ISTANBUL" and "İstanbul" all resolve to the
// catalog spelling).
func canonical(v string, allowed []string) (string, bool) {
	key := foldTR(v)
	for _, a := range allowed {
		if foldTR(a) == key {
			return a, true
		}
	}
	return "", false
}

var trFolder = strings.NewReplacer(
	"İ", "i", "I", "i", "ı", "i",
	"Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c",
)

func foldTR(s string) string {
	return strings.ToLower(trFolder.Replace(strings.TrimSpace(s)))
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
