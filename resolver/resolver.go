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

// ============================================================================
// Type-Safe Field Resolution
// ============================================================================

// Field represents a typed configuration field that can be resolved.
// It supports static values, expressions, and binding references.
type Field struct {
	value interface{}
}

// Static creates a field with a static value
func Static(v interface{}) Field {
	return Field{value: v}
}

// Expr creates a field from an expression like {{bindings.dbConnection}}
func Expr(template string) Field {
	return Field{value: template}
}

// Binding creates a field that references a binding by name
func Binding(name string) Field {
	return Field{value: "{{bindings." + name + "}}"}
}

// FromConfig creates a field from a config value (auto-detects type)
func FromConfig(v interface{}) Field {
	return Field{value: v}
}

// String returns the resolved string value
func (f Field) String(r *Resolver) string {
	switch v := f.value.(type) {
	case string:
		return r.ResolveString(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// StringOr returns the resolved string value, or a default if empty
func (f Field) StringOr(r *Resolver, def string) string {
	s := f.String(r)
	if s == "" {
		return def
	}
	return s
}

// Int returns the resolved int value
func (f Field) Int(r *Resolver) (int, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		var i int
		_, err := fmt.Sscanf(resolved, "%d", &i)
		return i, err
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case nil:
		return 0, fmt.Errorf("field is nil")
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
}

// IntOr returns the resolved int value, or a default if error
func (f Field) IntOr(r *Resolver, def int) int {
	i, err := f.Int(r)
	if err != nil {
		return def
	}
	return i
}

// Int64 returns the resolved int64 value
func (f Field) Int64(r *Resolver) (int64, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		var i int64
		_, err := fmt.Sscanf(resolved, "%d", &i)
		return i, err
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case nil:
		return 0, fmt.Errorf("field is nil")
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// Float64 returns the resolved float64 value
func (f Field) Float64(r *Resolver) (float64, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		var f float64
		_, err := fmt.Sscanf(resolved, "%f", &f)
		return f, err
	case int:
		return float64(v), nil
	case float64:
		return v, nil
	case nil:
		return 0, fmt.Errorf("field is nil")
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

// Bool returns the resolved bool value
func (f Field) Bool(r *Resolver) (bool, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		resolved = strings.ToLower(strings.TrimSpace(resolved))
		return resolved == "true" || resolved == "1" || resolved == "yes", nil
	case bool:
		return v, nil
	case nil:
		return false, fmt.Errorf("field is nil")
	default:
		return false, fmt.Errorf("cannot convert %T to bool", v)
	}
}

// BoolOr returns the resolved bool value, or a default if error
func (f Field) BoolOr(r *Resolver, def bool) bool {
	b, err := f.Bool(r)
	if err != nil {
		return def
	}
	return b
}

// Map returns the resolved map value
func (f Field) Map(r *Resolver) (map[string]interface{}, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(resolved), &m); err != nil {
			return nil, err
		}
		return m, nil
	case map[string]interface{}:
		return r.ResolveMap(v), nil
	case nil:
		return nil, fmt.Errorf("field is nil")
	default:
		return nil, fmt.Errorf("cannot convert %T to map", v)
	}
}

// Slice returns the resolved slice value
func (f Field) Slice(r *Resolver) ([]interface{}, error) {
	switch v := f.value.(type) {
	case string:
		resolved := r.ResolveString(v)
		var s []interface{}
		if err := json.Unmarshal([]byte(resolved), &s); err != nil {
			return nil, err
		}
		return s, nil
	case []interface{}:
		return v, nil
	case nil:
		return nil, fmt.Errorf("field is nil")
	default:
		return nil, fmt.Errorf("cannot convert %T to slice", v)
	}
}

// Raw returns the raw (unresolved) value
func (f Field) Raw() interface{} {
	return f.value
}

// IsNil returns true if the field is nil
func (f Field) IsNil() bool {
	return f.value == nil
}

// IsExpr returns true if the field is an expression (contains {{}})
func (f Field) IsExpr() bool {
	s, ok := f.value.(string)
	if !ok {
		return false
	}
	return strings.Contains(s, "{{") && strings.Contains(s, "}}")
}

// IsBinding returns true if the field references a binding
func (f Field) IsBinding() bool {
	s, ok := f.value.(string)
	if !ok {
		return false
	}
	return strings.HasPrefix(s, "{{bindings.")
}

// GetBindingName returns the binding name if this is a binding reference
func (f Field) GetBindingName() string {
	s, ok := f.value.(string)
	if !ok {
		return ""
	}
	if !strings.HasPrefix(s, "{{bindings.") {
		return ""
	}
	// Extract name from {{bindings.name}}
	end := strings.Index(s, "}}")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(s[11:end])
}

// ============================================================================
// Typed Config for Skills
// ============================================================================

// TypedConfig provides type-safe access to node configuration
type TypedConfig struct {
	config   map[string]interface{}
	resolver *Resolver
}

// NewTypedConfig creates a typed config wrapper
func NewTypedConfig(config map[string]interface{}, resolver *Resolver) *TypedConfig {
	return &TypedConfig{config: config, resolver: resolver}
}

// Get returns a field by name
func (c *TypedConfig) Get(name string) Field {
	if c.config == nil {
		return Field{}
	}
	return FromConfig(c.config[name])
}

// String returns a resolved string field
func (c *TypedConfig) String(name string) string {
	return c.Get(name).String(c.resolver)
}

// StringOr returns a resolved string field with default
func (c *TypedConfig) StringOr(name, def string) string {
	return c.Get(name).StringOr(c.resolver, def)
}

// Int returns a resolved int field
func (c *TypedConfig) Int(name string) (int, error) {
	return c.Get(name).Int(c.resolver)
}

// IntOr returns a resolved int field with default
func (c *TypedConfig) IntOr(name string, def int) int {
	return c.Get(name).IntOr(c.resolver, def)
}

// Bool returns a resolved bool field
func (c *TypedConfig) Bool(name string) (bool, error) {
	return c.Get(name).Bool(c.resolver)
}

// BoolOr returns a resolved bool field with default
func (c *TypedConfig) BoolOr(name string, def bool) bool {
	return c.Get(name).BoolOr(c.resolver, def)
}

// Map returns a resolved map field
func (c *TypedConfig) Map(name string) (map[string]interface{}, error) {
	return c.Get(name).Map(c.resolver)
}

// Slice returns a resolved slice field
func (c *TypedConfig) Slice(name string) ([]interface{}, error) {
	return c.Get(name).Slice(c.resolver)
}

// Binding returns a binding value directly by name
func (c *TypedConfig) Binding(name string) interface{} {
	return c.resolver.GetBinding(name)
}

// BindingString returns a binding as string
func (c *TypedConfig) BindingString(name string) string {
	v := c.resolver.GetBinding(name)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// Raw returns the raw config map
func (c *TypedConfig) Raw() map[string]interface{} {
	return c.config
}