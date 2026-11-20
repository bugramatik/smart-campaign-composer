// Package schema builds JSON schemas in the subset structured outputs accept:
// closed objects, every property required, optional values as nullable types,
// no numeric or length constraints.
package schema

import "slices"

type M = map[string]any

// Object is a closed object schema with every property required.
func Object(props M) M {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	slices.Sort(required)
	return M{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func String() M                 { return M{"type": "string"} }
func Enum(values []string) M    { return M{"type": "string", "enum": values} }
func Const(value string) M      { return M{"type": "string", "const": value} }
func ArrayOf(items M) M         { return M{"type": "array", "items": items} }
func Nullable(typ string) M     { return M{"type": []string{typ, "null"}} }
func NullableEnum(v []string) M { return M{"anyOf": []any{Enum(v), M{"type": "null"}}} }
func AnyOf(variants ...M) M     { return M{"anyOf": toAny(variants)} }

func toAny(ms []M) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}
