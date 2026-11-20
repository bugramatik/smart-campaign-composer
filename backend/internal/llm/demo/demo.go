// Package demo is a keyword-based stand-in for Claude, used when no API key
// is configured so the whole product can be run and clicked through offline.
// It reads the same prompts the real model gets and streams schema-shaped
// JSON. It is intentionally simple; it is not the product's intelligence.
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"scc/internal/content"
	"scc/internal/llm"
	"scc/internal/segment"
)

type Client struct {
	// ChunkDelay paces the stream so the UI shows text arriving.
	ChunkDelay time.Duration
}

func New() *Client { return &Client{ChunkDelay: 20 * time.Millisecond} }

func (c *Client) Provider() string { return "demo" }
func (c *Client) Model() string    { return "demo-heuristic" }

func (c *Client) Stream(ctx context.Context, req llm.Request, h llm.StreamHandler) (llm.Response, error) {
	prompt := req.Messages[0].Text
	var out any
	switch req.Purpose {
	case "segment":
		out = proposeSegment(tag(prompt, "brief"), tag(prompt, "catalog"))
	case "content":
		out = writeContent(tag(prompt, "brief"), strings.Split(tag(prompt, "languages"), ","))
	default:
		return llm.Response{}, fmt.Errorf("demo llm: unknown purpose %q", req.Purpose)
	}
	text, _ := json.Marshal(out)

	r := []rune(string(text))
	for i := 0; i < len(r); i += 14 {
		select {
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		case <-time.After(c.ChunkDelay):
		}
		if h.Delta != nil {
			h.Delta(string(r[i:min(i+14, len(r))]))
		}
	}
	return llm.Response{Text: string(text), StopReason: "end_turn", Model: c.Model()}, nil
}

func tag(s, name string) string {
	open, close := "<"+name+">", "</"+name+">"
	i := strings.Index(s, open)
	j := strings.Index(s, close)
	if i < 0 || j < i {
		return ""
	}
	return strings.TrimSpace(s[i+len(open) : j])
}

var (
	daysRe     = regexp.MustCompile(`(?:son|last)\s+(\d{1,3})\s*(?:gün|gun|day)`)
	discountRe = regexp.MustCompile(`%\s?(\d{1,2})|(\d{1,2})\s?%`)
	ageRangeRe = regexp.MustCompile(`(\d{2})\s*(?:-|–|ile)\s*(\d{2})\s*(?:yas|age)`)
	ageMinRe   = regexp.MustCompile(`(\d{2})\s*yas(?:indan|in)?\s*(?:ustu|uzeri|ve uzeri|buyuk)`)
)

// Folded (ASCII, lower-case) keywords for closed-set catalog values.
var (
	tierWords = []struct{ word, tier string }{
		{"platin", "platinum"}, {"altin", "gold"}, {"gold", "gold"},
		{"gumus", "silver"}, {"silver", "silver"}, {"bronz", "bronze"},
	}
	categoryWords = []struct{ word, category string }{
		{"elektronik", "electronics"}, {"electronic", "electronics"},
		{"moda", "fashion"}, {"giyim", "fashion"}, {"fashion", "fashion"},
		{"ev urun", "home"}, {"dekorasyon", "home"}, {"home", "home"},
		{"guzellik", "beauty"}, {"kozmetik", "beauty"}, {"beauty", "beauty"},
		{"spor", "sports"}, {"sport", "sports"},
	}
	categoryTR = map[string]string{"electronics": "elektronik", "fashion": "moda", "home": "ev", "beauty": "güzellik", "sports": "spor"}
)

func proposeSegment(brief, catalogJSON string) segment.Proposal {
	var cat struct {
		Fields []segment.Field `json:"fields"`
	}
	_ = json.Unmarshal([]byte(catalogJSON), &cat)
	b := fold(brief)

	days := 30
	if m := daysRe.FindStringSubmatch(b); m != nil {
		days, _ = strconv.Atoi(m[1])
	}
	p := segment.Proposal{
		Rule:        segment.Rule{Match: segment.MatchAll},
		Assumptions: []string{"Demo modu: kural, anahtar kelime eşleştirmesiyle üretildi (ANTHROPIC_API_KEY tanımlı değil)."},
		Unsupported: []string{},
	}
	var summary []string
	add := func(c segment.Condition, s string) {
		p.Rule.Conditions = append(p.Rule.Conditions, c)
		summary = append(summary, s)
	}

	for _, f := range cat.Fields {
		if f.Name != "city" {
			continue
		}
		var hits []string
		for _, city := range f.Values {
			if strings.Contains(b, fold(city)) {
				hits = append(hits, city)
			}
		}
		if len(hits) > 0 {
			add(segment.Condition{Kind: segment.KindText, Field: "city", Operator: "in", Values: hits}, strings.Join(hits, " veya ")+" şehrinde yaşayan")
		}
	}

	var tiers []string
	for _, t := range tierWords {
		if strings.Contains(b, t.word) && !slices.Contains(tiers, t.tier) {
			tiers = append(tiers, t.tier)
		}
	}
	if len(tiers) > 0 {
		add(segment.Condition{Kind: segment.KindText, Field: "loyalty_tier", Operator: "in", Values: tiers}, strings.Join(tiers, "/")+" üyelik seviyesindeki")
	}

	if m := ageRangeRe.FindStringSubmatch(b); m != nil {
		lo, _ := strconv.ParseFloat(m[1], 64)
		hi, _ := strconv.ParseFloat(m[2], 64)
		add(segment.Condition{Kind: segment.KindNumber, Field: "age", Operator: "between", Value: &lo, ValueTo: &hi}, fmt.Sprintf("%s–%s yaş arası", m[1], m[2]))
	} else if m := ageMinRe.FindStringSubmatch(b); m != nil {
		lo, _ := strconv.ParseFloat(m[1], 64)
		add(segment.Condition{Kind: segment.KindNumber, Field: "age", Operator: "gte", Value: &lo}, m[1]+" yaş ve üzeri")
	}

	// A category narrows behaviour ("elektronik ürünleri inceleyen"); on its
	// own it usually names the offer, not the audience.
	var category *string
	for _, c := range categoryWords {
		if strings.Contains(b, c.word) {
			category = &c.category
			break
		}
	}
	inCat := func(s string) string {
		if category == nil {
			return s
		}
		return categoryTR[*category] + " kategorisinde " + s
	}

	yes, no := true, false
	window := &days
	if containsAny(b, "sepet", "cart") {
		add(segment.Condition{Kind: segment.KindEvent, Event: "cart_add", Occurred: &yes, WithinDays: window},
			fmt.Sprintf("son %d günde sepete ürün ekleyen", days))
	}
	if containsAny(b, "incele", "goruntule", "goz atan", "bakan", "viewed", "browsed") {
		add(segment.Condition{Kind: segment.KindEvent, Event: "product_view", Occurred: &yes, WithinDays: window, Category: category},
			fmt.Sprintf("son %d günde %s", days, inCat("ürün inceleyen")))
	}
	switch {
	case containsAny(b, "satin almayan", "satin almamis", "alisveris yapmayan", "alisveris yapmamis", "almayan", "almamis",
		"geri kazan", "not purchased", "haven't bought", "did not buy", "win back"):
		add(segment.Condition{Kind: segment.KindEvent, Event: "purchase", Occurred: &no, WithinDays: window},
			fmt.Sprintf("son %d günde satın alma yapmamış", days))
	case containsAny(b, "satin alan", "satin almis", "alisveris yapan", "purchased", "bought"):
		add(segment.Condition{Kind: segment.KindEvent, Event: "purchase", Occurred: &yes, WithinDays: window, Category: category},
			fmt.Sprintf("son %d günde %s", days, inCat("satın alma yapmış")))
	}
	if len(p.Rule.Conditions) == 0 {
		add(segment.Condition{Kind: segment.KindEvent, Event: "page_view", Occurred: &yes, WithinDays: window},
			fmt.Sprintf("son %d günde siteyi ziyaret etmiş", days))
		p.Assumptions = append(p.Assumptions, "Brief'te tanınan bir hedefleme kriteri bulunamadı; son dönemde aktif kullanıcılar varsayıldı.")
	}
	if strings.Contains(b, "48 saat") || strings.Contains(b, "erken erisim") {
		p.Unsupported = append(p.Unsupported, "Erken erişim süresi bir hedefleme kuralı değil; mesaj metninde belirtildi.")
	}

	if containsAny(b, "sms") {
		p.Channels = append(p.Channels, "sms")
	}
	if containsAny(b, "e-posta", "eposta", "email", "e-mail", "mail") {
		p.Channels = append(p.Channels, "email")
	}
	if len(p.Channels) == 0 {
		p.Channels = []string{"sms", "email"}
	}
	p.Summary = upperFirst(strings.Join(summary, ", ") + " kullanıcılar.")
	return p
}

// offer is one campaign theme in both languages; the demo picks the theme
// that best matches the brief.
type offer struct {
	smsTR, subjectTR, preTR, bodyTR string
	smsEN, subjectEN, preEN, bodyEN string
}

func pickOffer(brief string) offer {
	b := fold(brief)
	rate := ""
	if m := discountRe.FindStringSubmatch(brief); m != nil {
		rate = m[1] + m[2]
	}
	switch {
	case containsAny(b, "2 al 1 ode", "3 al 2 ode", "buy 2"):
		return offer{
			"{{first_name}}, hafta sonuna özel 2 al 1 öde fırsatı başladı! Favorilerini seç, birini bizden al.",
			"{{first_name}}, hafta sonu 2 al 1 öde başladı", "Sepetine iki ürün ekle, birini ödeme.",
			"Bu hafta sonu seçili ürünlerde 2 al 1 öde fırsatı seni bekliyor. İki ürün seç, daha uygun olanı bizden.",
			"{{first_name}}, buy 2 get 1 free starts this weekend! Pick your favourites, one is on us.",
			"{{first_name}}, buy 2 get 1 free this weekend", "Add two items, pay for one.",
			"This weekend selected items are buy 2 get 1 free. Pick two, the cheaper one is on us.",
		}
	case containsAny(b, "erken erisim", "yeni sezon", "koleksiyon", "early access"):
		return offer{
			"{{first_name}}, yeni sezon koleksiyonu sana 48 saat erken açıldı. Herkesten önce keşfet!",
			"{{first_name}}, yeni sezon sana erken açıldı", "Koleksiyonu herkesten 48 saat önce keşfet.",
			"Sadık üyelerimize teşekkür olarak yeni sezon koleksiyonunu 48 saat erken açtık. Beğendiklerini stoklar tükenmeden ayırt.",
			"{{first_name}}, the new season collection is open to you 48 hours early. Shop before anyone else!",
			"{{first_name}}, early access to the new season", "Explore the collection 48 hours before everyone else.",
			"As a thank-you to our members, the new season collection opens to you 48 hours early. Grab your favourites before they sell out.",
		}
	case rate != "" && containsAny(b, "sepet", "cart"):
		return offer{
			"{{first_name}}, sepetine özel %" + rate + " indirim seni bekliyor! Alışverişini uygulamamızdan tamamla.",
			"{{first_name}}, sepetine özel %" + rate + " indirim", "Seçtiğin ürünler hâlâ sepetinde. Kaçırmadan tamamla.",
			"Sepetine eklediğin ürünler hâlâ seni bekliyor ve şimdi %" + rate + " indirimli. Seçtiğin ürünleri senin için ayırdık.",
			"{{first_name}}, a " + rate + "% discount is waiting in your cart! Complete your order in our app.",
			"{{first_name}}, " + rate + "% off what's in your cart", "The items you picked are still in your cart.",
			"The items in your cart are still waiting, now " + rate + "% off. We saved them for you.",
		}
	case containsAny(b, "kargo bedava", "ucretsiz kargo", "free shipping"):
		return offer{
			"{{first_name}}, seni özledik! Bir sonraki siparişinde kargo bizden. Yeni gelenlere göz at.",
			"{{first_name}}, seni özledik: kargo bizden", "Bir sonraki siparişinde ücretsiz kargo.",
			"Bir süredir görüşemedik. İncelediğin ürünlerde yeni modeller var ve bir sonraki siparişinde kargo ücretsiz.",
			"{{first_name}}, we miss you! Shipping is on us for your next order. See what's new.",
			"{{first_name}}, we miss you: free shipping", "Free shipping on your next order.",
			"It has been a while. There are new arrivals in the products you viewed, and your next order ships free.",
		}
	case rate != "":
		return offer{
			"{{first_name}}, sana özel %" + rate + " indirim tanımlandı! Uygulamamızda hemen kullanabilirsin.",
			"{{first_name}}, sana özel %" + rate + " indirim", "İndirimin hesabına tanımlandı.",
			"Sana özel %" + rate + " indirim hesabına tanımlandı. Uygulamamızda dilediğin ürünlerde kullanabilirsin.",
			"{{first_name}}, you have " + rate + "% off, just for you! Use it in our app today.",
			"{{first_name}}, " + rate + "% off just for you", "Your discount is ready in your account.",
			"A " + rate + "% discount has been added to your account. Use it on anything in our app.",
		}
	default:
		return offer{
			"{{first_name}}, sepetindeki ürünler seni bekliyor! Alışverişini uygulamamızdan tamamlayabilirsin.",
			"{{first_name}}, sepetindeki ürünler seni bekliyor", "Seçtiğin ürünler hâlâ sepetinde. Kaçırmadan tamamla.",
			"Seçtiğin ürünleri senin için ayırdık. Alışverişini uygulamamızdan birkaç adımda tamamlayabilirsin.",
			"{{first_name}}, your cart is waiting for you! Complete your order in our app.",
			"{{first_name}}, your cart is waiting", "The items you picked are still in your cart.",
			"We saved the items you picked. You can complete your order in our app in a few steps.",
		}
	}
}

func writeContent(brief string, languages []string) content.Content {
	o := pickOffer(brief)
	var out content.Content
	for _, lang := range languages {
		switch strings.TrimSpace(lang) {
		case "tr":
			out.Variants = append(out.Variants, content.Variant{
				Language: "tr", SMS: o.smsTR, EmailSubject: o.subjectTR, EmailPreheader: o.preTR,
				EmailBody: "Merhaba {{first_name}},\n\n" + o.bodyTR + "\n\nSevgiler,\nEkibimiz",
			})
		case "en":
			out.Variants = append(out.Variants, content.Variant{
				Language: "en", SMS: o.smsEN, EmailSubject: o.subjectEN, EmailPreheader: o.preEN,
				EmailBody: "Hi {{first_name}},\n\n" + o.bodyEN + "\n\nBest,\nThe team",
			})
		}
	}
	return out
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

var folder = strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c")

func fold(s string) string { return strings.ToLower(folder.Replace(s)) }

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
