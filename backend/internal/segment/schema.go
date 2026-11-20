package segment

import "scc/internal/schema"

// ProposalSchema is the structured-output JSON schema for a Proposal.
//
// Structured outputs reject numeric/length constraints and recursion, so the
// schema only fixes the shape and the closed vocabularies; ranges and the city
// list are enforced by Validate and fed back to the model on failure.
func (c Catalog) ProposalSchema() map[string]any {
	textCond := schema.Object(schema.M{
		"kind":     schema.Const(KindText),
		"field":    schema.Enum(c.fieldNames(TypeText)),
		"operator": schema.Enum(TextOperators),
		"values":   schema.ArrayOf(schema.String()),
	})
	numberCond := schema.Object(schema.M{
		"kind":     schema.Const(KindNumber),
		"field":    schema.Enum(c.fieldNames(TypeNumber)),
		"operator": schema.Enum(NumberOperators),
		"value":    schema.M{"type": "number"},
		"value_to": schema.Nullable("number"),
	})
	eventCond := schema.Object(schema.M{
		"kind":        schema.Const(KindEvent),
		"event":       schema.Enum(c.Events),
		"occurred":    schema.M{"type": "boolean"},
		"within_days": schema.Nullable("integer"),
		"min_count":   schema.Nullable("integer"),
		"category":    schema.NullableEnum(c.Categories),
	})
	return schema.Object(schema.M{
		"rule": schema.Object(schema.M{
			"match":      schema.Enum([]string{MatchAll, MatchAny}),
			"conditions": schema.ArrayOf(schema.AnyOf(textCond, numberCond, eventCond)),
		}),
		"channels":    schema.ArrayOf(schema.Enum(Channels)),
		"summary":     schema.String(),
		"assumptions": schema.ArrayOf(schema.String()),
		"unsupported": schema.ArrayOf(schema.String()),
	})
}
