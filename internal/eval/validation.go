package eval

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type Decision struct {
	Outcome   string                     `json:"outcome"`
	Tool      string                     `json:"tool,omitempty"`
	Arguments map[string]json.RawMessage `json:"arguments,omitempty"`
	Missing   []string                   `json:"missing,omitempty"`
	Reason    string                     `json:"reason,omitempty"`
}

func validate(request, raw string, candidates []Tool) Decision {
	reject := func(reason string) Decision { return Decision{Outcome: "rejected", Reason: reason} }
	// This intentionally conservative diagnostic policy is not a production path
	// validator. No field is ever used as an executable argument in this harness.
	if strings.ContainsAny(request, ";|>`\n\r") || strings.Contains(request, "$(") {
		return reject("shell syntax is outside this diagnostic's allowed request syntax")
	}
	var response struct {
		Type      string          `json:"type"`
		Success   bool            `json:"success"`
		Error     json.RawMessage `json:"error"`
		ErrorCode json.RawMessage `json:"error_code"`
		Calls     []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function_calls"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return reject("malformed model JSON")
	}
	if !response.Success || strings.TrimSpace(string(response.Error)) != "null" || strings.TrimSpace(string(response.ErrorCode)) != "null" {
		return reject("model reported failure or omitted its success envelope")
	}
	if response.Type != "call" {
		return reject("unexpected response type: " + response.Type)
	}
	if len(response.Calls) == 0 {
		return Decision{Outcome: "no_call", Reason: "empty or suppressed call is not authority to choose a tool"}
	}
	if len(response.Calls) != 1 {
		return reject("multiple calls are outside this diagnostic")
	}
	call := response.Calls[0]
	var tool *Tool
	for i := range candidates {
		if candidates[i].Name == call.Name {
			tool = &candidates[i]
			break
		}
	}
	if tool == nil {
		return reject("tool is not in the trusted, enabled shortlist")
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil || arguments == nil {
		return reject("arguments must be an explicit object")
	}
	for name, value := range arguments {
		kind, ok := tool.Fields[name]
		if !ok {
			return reject("unknown argument: " + name)
		}
		var text string
		switch kind {
		case "string":
			if err := json.Unmarshal(value, &text); err != nil || text == "" || strings.ContainsRune(text, 0) || strings.HasPrefix(text, "-") {
				return reject("invalid string argument: " + name)
			}
		case "integer":
			var n int
			if err := json.Unmarshal(value, &n); err != nil || n <= 0 || (name == "port" && n > 65535) {
				return reject("invalid integer argument: " + name)
			}
			text = strconv.Itoa(n)
		default:
			return reject("unknown fixture field type")
		}
		if !grounded(request, text) {
			return reject("argument was not explicitly supplied: " + name)
		}
	}
	decision := Decision{Outcome: "proposal_validated", Tool: tool.Name, Arguments: arguments}
	for _, name := range tool.Required {
		if _, ok := arguments[name]; !ok {
			decision.Missing = append(decision.Missing, name)
		}
	}
	if len(decision.Missing) > 0 {
		decision.Outcome = "needs_input"
		decision.Reason = fmt.Sprintf("Which %s should I use?", strings.Join(decision.Missing, " and "))
	}
	return decision
}

// Literal token evidence is deliberately narrow. It does not resolve paths,
// derive defaults, or establish that the proposed action matches user intent.
func grounded(request, value string) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	for offset := 0; offset <= len(request)-len(value); {
		i := strings.Index(request[offset:], value)
		if i < 0 {
			return false
		}
		i += offset
		before, after := []rune(request[:i]), []rune(request[i+len(value):])
		if (len(before) == 0 || !word(before[len(before)-1])) && (len(after) == 0 || !word(after[0])) {
			return true
		}
		offset = i + 1
	}
	return false
}
