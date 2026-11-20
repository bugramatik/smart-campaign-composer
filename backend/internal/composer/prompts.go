package composer

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"scc/internal/content"
	"scc/internal/segment"
	"scc/internal/validation"
)

// Prompt versions are stored with every LLM call so output changes can be
// traced to prompt changes.
const (
	SegmentPromptVersion = "segment-v1"
	ContentPromptVersion = "content-v1"
)

const segmentSystem = `You turn a marketer's campaign brief into an audience rule for an e-commerce CRM.
Reply with JSON that matches the provided schema. The backend validates the rule against the catalog and compiles it to SQL itself, so use only the fields, events, categories and values listed in <catalog>.

Rule semantics
- rule.match "all" means every condition must hold (AND); "any" means at least one must hold (OR).
- text condition: field "in" / "not_in" values. Values must be spelled as in the catalog.
- number condition: field compared with value. "between" is inclusive and uses value_to; value_to is null for every other operator.
- event condition: whether the user has (occurred=true) or has not (occurred=false) done the event, optionally only within the last within_days days (null = ever) and only in one product category (null = any category). min_count is null (at least once) unless the brief asks for a number of times, and is only valid with occurred=true.

Examples of the mapping
- "son 30 günde sepete ürün ekleyip satın almayan" -> cart_add occurred=true within_days=30 AND purchase occurred=false within_days=30.
- "en az 3 kez alışveriş yapmış" -> purchase occurred=true min_count=3, within_days=null.
- "Ankara veya İzmir'deki" -> one text condition city in ["Ankara", "İzmir"], not two conditions.

How to interpret the brief
- Map every targeting criterion in the brief to a condition, and add none the brief does not ask for.
- If a phrase is ambiguous, choose the most reasonable reading and record it in assumptions, e.g. which events a time window applies to.
- If a criterion cannot be expressed with the catalog (an unknown attribute, a guess about intent, a product name, past campaign membership), leave it out of the rule and explain it in unsupported. Approximating it silently is worse than leaving it out.
- Do not add consent or opt-in conditions: delivery filters each channel by consent on its own.
- channels: the channels the brief asks for; both when it names none.
- summary: one sentence a marketer reads to confirm who will receive the campaign.
- Write summary, assumptions and unsupported in Turkish.`

const contentSystem = `You write campaign messages for an e-commerce brand: one SMS and one email for each audience language.
Reply with JSON that matches the provided schema. The backend checks every rule below and sends violations back to you.

Hard limits, counted after each variable is replaced by the longest value in the audience (see <limits>)
- sms: at most 160 characters.
- email_subject: at most 80 characters.
- email_preheader: at most 140 characters.
- email_body: at most 4000 characters, plain text, short paragraphs separated by blank lines, no HTML or Markdown.
Keep a margin of a few characters under each limit.

Personalization
- Only {{first_name}}, {{last_name}} and {{city}} exist. Write them exactly like that; no other placeholders and no single braces.

Languages
- Exactly one variant per language listed in <languages>. "tr" is written in Turkish, "en" in English; both carry the same offer and facts.

Content
- Use only facts given in the brief (discounts, codes, deadlines, links, product categories). Never invent a discount rate, coupon code, date or URL. Without a link in the brief, point to "uygulamamız" / "our app" instead of a URL.
- Match the audience described in <audience>: the message should make sense for every person in it.
- Warm and concise. No all-caps words, at most one emoji per SMS, one clear call to action.
- Fill every field, even for channels the campaign may not use.`

type catalogView struct {
	Fields     []segment.Field `json:"fields"`
	Events     []string        `json:"events"`
	Categories []string        `json:"categories"`
	MaxDays    int             `json:"max_within_days"`
}

func segmentUserPrompt(brief string, cat segment.Catalog, now time.Time) string {
	catJSON, _ := json.Marshal(catalogView{Fields: cat.Fields, Events: cat.Events, Categories: cat.Categories, MaxDays: cat.MaxDays})
	return fmt.Sprintf("<today>%s</today>\n<catalog>%s</catalog>\n<brief>\n%s\n</brief>", now.Format("2006-01-02"), catJSON, brief)
}

// ContentInput is everything the content step needs about the campaign.
type ContentInput struct {
	Brief      string
	Summary    string
	Languages  []string
	MaxLengths map[string]int
	Feedback   string           // marketer's request when regenerating
	Previous   *content.Content // content being revised, if any
}

func contentUserPrompt(in ContentInput) string {
	limits, _ := json.Marshal(map[string]any{
		"sms": content.MaxSMS, "email_subject": content.MaxEmailSubject,
		"email_preheader": content.MaxPreheader, "email_body": content.MaxEmailBody,
		"longest_variable_values": in.MaxLengths,
	})
	var b strings.Builder
	fmt.Fprintf(&b, "<languages>%s</languages>\n<limits>%s</limits>\n", strings.Join(in.Languages, ","), limits)
	fmt.Fprintf(&b, "<audience>%s</audience>\n<brief>\n%s\n</brief>", in.Summary, in.Brief)
	if in.Previous != nil {
		prev, _ := json.Marshal(in.Previous)
		fmt.Fprintf(&b, "\n<current_content>%s</current_content>", prev)
	}
	if in.Feedback != "" {
		fmt.Fprintf(&b, "\n<marketer_feedback>\n%s\n</marketer_feedback>\nRevise the current content according to the feedback; keep what the feedback does not mention.", in.Feedback)
	}
	return b.String()
}

func repairPrompt(issues []validation.Issue) string {
	var b strings.Builder
	b.WriteString("The backend rejected your previous answer:\n")
	for _, is := range issues {
		b.WriteString("- " + is.String() + "\n")
	}
	b.WriteString("Return the complete corrected JSON. Fix every listed problem and keep everything else unchanged.")
	return b.String()
}
