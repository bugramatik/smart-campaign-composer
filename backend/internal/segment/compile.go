package segment

import (
	"fmt"
	"strconv"
	"strings"
)

// Compiled is a WHERE fragment over `users u` plus its positional arguments.
// Every identifier in SQL comes from the catalog; every user-controlled value
// is a bind parameter.
type Compiled struct {
	Where string
	Args  []any
}

// Compile turns a validated rule into SQL. argOffset is the number of bind
// parameters the caller already uses, so the fragment can be embedded.
// It re-checks the catalog and fails closed instead of trusting the caller.
func (c Catalog) Compile(r Rule, argOffset int) (Compiled, error) {
	b := builder{offset: argOffset}
	parts := make([]string, 0, len(r.Conditions))
	for i, cond := range r.Conditions {
		sql, err := c.compileCondition(&b, cond)
		if err != nil {
			return Compiled{}, fmt.Errorf("condition %d: %w", i, err)
		}
		parts = append(parts, sql)
	}
	if len(parts) == 0 {
		return Compiled{}, fmt.Errorf("rule has no conditions")
	}
	var joiner string
	switch r.Match {
	case MatchAll:
		joiner = " AND "
	case MatchAny:
		joiner = " OR "
	default:
		return Compiled{}, fmt.Errorf("invalid match %q", r.Match)
	}
	return Compiled{Where: "(" + strings.Join(parts, joiner) + ")", Args: b.args}, nil
}

func (c Catalog) compileCondition(b *builder, cond Condition) (string, error) {
	switch cond.Kind {
	case KindText:
		f, ok := c.field(cond.Field)
		if !ok || f.Type != TypeText {
			return "", fmt.Errorf("unknown text field %q", cond.Field)
		}
		p := b.arg(cond.Values)
		switch cond.Operator {
		case "in":
			return fmt.Sprintf("%s = ANY(%s::text[])", f.expr, p), nil
		case "not_in":
			return fmt.Sprintf("NOT (%s = ANY(%s::text[]))", f.expr, p), nil
		}
		return "", fmt.Errorf("invalid text operator %q", cond.Operator)

	case KindNumber:
		f, ok := c.field(cond.Field)
		if !ok || f.Type != TypeNumber || cond.Value == nil {
			return "", fmt.Errorf("invalid number condition on %q", cond.Field)
		}
		ops := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
		if op, ok := ops[cond.Operator]; ok {
			return fmt.Sprintf("%s %s %s::numeric", f.expr, op, b.arg(*cond.Value)), nil
		}
		if cond.Operator == "between" && cond.ValueTo != nil {
			return fmt.Sprintf("%s BETWEEN %s::numeric AND %s::numeric", f.expr, b.arg(*cond.Value), b.arg(*cond.ValueTo)), nil
		}
		return "", fmt.Errorf("invalid number operator %q", cond.Operator)

	case KindEvent:
		if !contains(c.Events, cond.Event) || cond.Occurred == nil {
			return "", fmt.Errorf("invalid event condition %q", cond.Event)
		}
		filter := "e.user_id = u.id AND e.type = " + b.arg(cond.Event)
		if cond.WithinDays != nil {
			filter += fmt.Sprintf(" AND e.occurred_at >= now() - make_interval(days => %s::int)", b.arg(*cond.WithinDays))
		}
		if cond.Category != nil {
			if !contains(c.Categories, *cond.Category) {
				return "", fmt.Errorf("unknown category %q", *cond.Category)
			}
			filter += " AND e.category = " + b.arg(*cond.Category)
		}
		if cond.MinCount != nil && *cond.MinCount > 1 {
			return fmt.Sprintf("(SELECT count(*) FROM events e WHERE %s) >= %s::int", filter, b.arg(*cond.MinCount)), nil
		}
		exists := "EXISTS (SELECT 1 FROM events e WHERE " + filter + ")"
		if !*cond.Occurred {
			return "NOT " + exists, nil
		}
		return exists, nil
	}
	return "", fmt.Errorf("unknown kind %q", cond.Kind)
}

type builder struct {
	offset int
	args   []any
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(b.offset+len(b.args))
}
