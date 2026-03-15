// Package resolver provides template resolution for Axiom skills.
// It handles {{path.to.value}} template expressions.
package resolver

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config holds the data needed for template resolution
type Config struct {
	// Trigger data from the workflow trigger
	Trigger map[string]interface{}

	// Bindings resolved from agent definition
	Bindings map[string]interface{}

	// Previous node's output
	Prev map[string]interface{}

	// All node outputs by node name
	Nodes map[string]interface{}

	// Variables set by set nodes
	Variables map[string]interface{}

	// Current node context
	Self map[string]interface{}

	// Run metadata
	Run map[string]interface{}

	// Elapsed time in milliseconds
	ElapsedMs int64
}

// Resolver implements executor.TemplateResolver with full template support
type Resolver struct {
	config Config
}

// New creates a new resolver with the given config
func New(config Config) *Resolver {
	return &Resolver{config: config}
}

// ResolveString resolves {{path.to.value}} templates in a string
func (r *Resolver) ResolveString(template string) string {
	result := template
	for {
		start := findTemplateStart(result)
		if start == -1 {
			break
		}
		end := findTemplateEnd(result, start)
		if end == -1 {
			break
		}

		// Trim whitespace from path to handle {{ nodes.x }} style templates
		path := strings.TrimSpace(result[start+2 : end])
		value := r.resolvePath(path)

		replacement := ""
		if value != nil {
			switch v := value.(type) {
			case string:
				replacement = v
			case int, int64, int32, float64, float32, bool:
				replacement = fmt.Sprintf("%v", v)
			default:
				if jsonBytes, err := json.Marshal(value); err == nil {
					replacement = string(jsonBytes)
				} else {
					replacement = fmt.Sprintf("%v", value)
				}
			}
		}

		result = result[:start] + replacement + result[end+2:]
	}
	return result
}

// ResolveMap resolves all string values in a map
func (r *Resolver) ResolveMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range input {
		switch val := v.(type) {
		case string:
			result[k] = r.ResolveString(val)
		case map[string]interface{}:
			result[k] = r.ResolveMap(val)
		default:
			result[k] = val
		}
	}
	return result
}

// EvaluateCondition evaluates a condition string
func (r *Resolver) EvaluateCondition(condition string) bool {
	// Handle {{value}} == expected format
	resolved := r.ResolveString(condition)

	// Check for equality
	if strings.Contains(resolved, "==") {
		parts := strings.SplitN(resolved, "==", 2)
		if len(parts) == 2 {
			left := strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])
			return left == right
		}
	}

	// Check for inequality
	if strings.Contains(resolved, "!=") {
		parts := strings.SplitN(resolved, "!=", 2)
		if len(parts) == 2 {
			left := strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])
			return left != right
		}
	}

	// Truthy check - non-empty string is true
	return resolved != "" && resolved != "false" && resolved != "0"
}

// SetVariable sets a variable
func (r *Resolver) SetVariable(name string, value interface{}) {
	if r.config.Variables == nil {
		r.config.Variables = make(map[string]interface{})
	}
	r.config.Variables[name] = value
}

// GetStepOutput returns the output of a previous step
func (r *Resolver) GetStepOutput(stepName string) interface{} {
	if r.config.Nodes == nil {
		return nil
	}
	return r.config.Nodes[stepName]
}

// SetStepOutput sets the output of a step
func (r *Resolver) SetStepOutput(stepName string, output interface{}) {
	if r.config.Nodes == nil {
		r.config.Nodes = make(map[string]interface{})
	}
	r.config.Nodes[stepName] = output
}

// GetBinding returns a binding value by name
func (r *Resolver) GetBinding(name string) interface{} {
	if r.config.Bindings == nil {
		return nil
	}
	return r.config.Bindings[name]
}

// GetBindings returns all bindings
func (r *Resolver) GetBindings() map[string]interface{} {
	return r.config.Bindings
}

// GetContextData returns all context data for code execution
func (r *Resolver) GetContextData() map[string]interface{} {
	return map[string]interface{}{
		"bindings":   r.config.Bindings,
		"trigger":    r.config.Trigger,
		"prev":       r.config.Prev,
		"nodes":      r.config.Nodes,
		"vars":       r.config.Variables,
		"run":        r.config.Run,
		"self":       r.config.Self,
		"elapsed_ms": r.config.ElapsedMs,
	}
}

// resolvePath resolves a dot-separated path to a value
func (r *Resolver) resolvePath(path string) interface{} {
	parts := splitPath(path)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "trigger":
		return getNestedValue(r.config.Trigger, parts[1:])
	case "bindings":
		return getNestedValue(r.config.Bindings, parts[1:])
	case "prev":
		return getNestedValue(r.config.Prev, parts[1:])
	case "self":
		return getNestedValue(r.config.Self, parts[1:])
	case "elapsed_ms":
		return r.config.ElapsedMs
	case "nodes":
		if len(parts) >= 2 {
			nodeOutput := r.config.Nodes[parts[1]]
			return getNestedValue(nodeOutput, parts[2:])
		}
		return r.config.Nodes
	case "var":
		if len(parts) >= 2 {
			return getNestedValue(r.config.Variables[parts[1]], parts[2:])
		}
		return r.config.Variables
	case "run":
		return getNestedValue(r.config.Run, parts[1:])
	}
	return nil
}

func findTemplateStart(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '{' && s[i+1] == '{' {
			return i
		}
	}
	return -1
}

func findTemplateEnd(s string, start int) int {
	for i := start + 2; i < len(s)-1; i++ {
		if s[i] == '}' && s[i+1] == '}' {
			return i
		}
	}
	return -1
}

func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func getNestedValue(data interface{}, path []string) interface{} {
	if len(path) == 0 || data == nil {
		return data
	}

	m, ok := data.(map[string]interface{})
	if !ok {
		return nil
	}

	value, ok := m[path[0]]
	if !ok {
		return nil
	}

	return getNestedValue(value, path[1:])
}