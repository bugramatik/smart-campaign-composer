package llm

import (
	"encoding/json"
)

// CompleteJSON turns a prefix of a JSON document into a valid document by
// closing whatever is open. An unterminated string value is kept (closed in
// place) so streamed text appears as it arrives; an unterminated key, number
// or literal is dropped back to the last complete value. ok is false when no
// valid prefix exists yet.
func CompleteJSON(s string) (out string, ok bool) {
	var (
		stack     []byte // open containers: '{' or '['
		expectKey []bool // per container: next string in this object is a key
		safeLen   = -1   // longest prefix that is valid once closed
		safeStack []byte
		inStr     bool
		esc       bool
		strIsKey  bool
		inPrim    bool
	)
	mark := func(n int) {
		safeLen = n
		safeStack = append(safeStack[:0], stack...)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
				if !strIsKey {
					mark(i + 1)
				}
			}
			continue
		}
		if inPrim {
			if !isDelim(c) {
				continue
			}
			inPrim = false
			mark(i)
		}
		switch c {
		case '{', '[':
			stack = append(stack, c)
			expectKey = append(expectKey, c == '{')
			mark(i + 1)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			stack, expectKey = stack[:len(stack)-1], expectKey[:len(expectKey)-1]
			mark(i + 1)
		case '"':
			inStr = true
			strIsKey = len(stack) > 0 && stack[len(stack)-1] == '{' && expectKey[len(expectKey)-1]
		case ':':
			if len(expectKey) > 0 {
				expectKey[len(expectKey)-1] = false
			}
		case ',':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				expectKey[len(expectKey)-1] = true
			}
		case ' ', '\t', '\r', '\n':
		default:
			inPrim = true
		}
	}

	if inStr && !strIsKey {
		// Close the open value string; back off over a dangling escape.
		closers := closing(stack)
		for cut := 0; cut <= 6 && cut < len(s); cut++ {
			cand := s[:len(s)-cut] + `"` + closers
			if json.Valid([]byte(cand)) {
				return cand, true
			}
		}
	}
	if inPrim && len(stack) == 0 && json.Valid([]byte(s)) {
		return s, true // a complete top-level primitive
	}
	if safeLen < 0 {
		return "", false
	}
	cand := s[:safeLen] + closing(safeStack)
	return cand, json.Valid([]byte(cand))
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ',', ']', '}', ':':
		return true
	}
	return false
}

func closing(stack []byte) string {
	out := make([]byte, len(stack))
	for i, c := range stack {
		if c == '{' {
			out[len(stack)-1-i] = '}'
		} else {
			out[len(stack)-1-i] = ']'
		}
	}
	return string(out)
}
