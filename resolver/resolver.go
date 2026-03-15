// Package resolver provides template resolution for Axiom skills.
// It handles {{path.to.value}} template expressions.
package resolver

import (
	"encoding/json"
	"fmt"
	"reflect"
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
// Type-Safe Config Resolution
// ============================================================================

// Expr marks a string field as an expression that should be resolved.
// Use it as a type alias: type Expr string
type Expr string

// Binding marks a string field as a binding reference.
// The value should be the binding name, and it will be resolved from bindings.
type Binding string

// ResolveConfig resolves a config map into a typed struct.
// String fields are automatically resolved for {{}} expressions.
// Fields of type Binding are resolved from bindings.
//
// Example:
//
//	type MyConfig struct {
//	    ConnectionString string   `json:"connectionString"` // Auto-resolved if contains {{}}
//	    DBConnection     Binding  `json:"dbConnection"`     // Resolved from bindings
//	    Timeout          int      `json:"timeout"`
//	    Query            Expr     `json:"query"`            // Always resolved as expression
//	}
//
//	var cfg MyConfig
//	err := resolver.ResolveConfig(config, &cfg, r)
func ResolveConfig(src map[string]interface{}, dst interface{}, r *Resolver) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Ptr || dstVal.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("dst must be a pointer to struct")
	}

	dstVal = dstVal.Elem()
	dstType := dstVal.Type()

	for i := 0; i < dstType.NumField(); i++ {
		field := dstType.Field(i)
		fieldVal := dstVal.Field(i)

		// Get JSON tag or use field name
		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			key = field.Name
		}
		// Remove omitempty etc
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		srcVal, exists := src[key]
		if !exists {
			// Check for default tag
			def := field.Tag.Get("default")
			if def != "" {
				if err := setFieldValue(fieldVal, def, field.Type, r); err != nil {
					return fmt.Errorf("field %s: %w", key, err)
				}
			}
			continue
		}

		if err := setFieldValue(fieldVal, srcVal, field.Type, r); err != nil {
			return fmt.Errorf("field %s: %w", key, err)
		}
	}

	return nil
}

func setFieldValue(field reflect.Value, src interface{}, fieldType reflect.Type, r *Resolver) error {
	// Handle different field types
	switch fieldType.Kind() {
	case reflect.String:
		s, err := toString(src)
		if err != nil {
			return err
		}
		// Auto-resolve if it's an expression or Binding type
		if strings.Contains(s, "{{") || fieldType.Name() == "Binding" || fieldType.Name() == "Expr" {
			s = r.ResolveString(s)
		}
		field.SetString(s)

	case reflect.Int, reflect.Int64, reflect.Int32:
		i, err := toInt(src)
		if err != nil {
			return err
		}
		field.SetInt(i)

	case reflect.Float64, reflect.Float32:
		f, err := toFloat(src)
		if err != nil {
			return err
		}
		field.SetFloat(f)

	case reflect.Bool:
		b, err := toBool(src)
		if err != nil {
			return err
		}
		field.SetBool(b)

	case reflect.Map:
		if src == nil {
			return nil
		}
		m, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map, got %T", src)
		}
		// Resolve any string values in the map
		resolved := r.ResolveMap(m)
		field.Set(reflect.ValueOf(resolved))

	case reflect.Slice:
		if src == nil {
			return nil
		}
		slice, ok := src.([]interface{})
		if !ok {
			return fmt.Errorf("expected slice, got %T", src)
		}
		field.Set(reflect.ValueOf(slice))

	case reflect.Interface:
		field.Set(reflect.ValueOf(src))

	default:
		// Try to set directly
		if src != nil {
			srcVal := reflect.ValueOf(src)
			if srcVal.Type().ConvertibleTo(fieldType) {
				field.Set(srcVal.Convert(fieldType))
			}
		}
	}

	return nil
}

func toString(src interface{}) (string, error) {
	if src == nil {
		return "", nil
	}
	switch v := src.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return fmt.Sprintf("%v", src), nil
	}
}

func toInt(src interface{}) (int64, error) {
	if src == nil {
		return 0, nil
	}
	switch v := src.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		var i int64
		_, err := fmt.Sscanf(v, "%d", &i)
		return i, err
	default:
		return 0, fmt.Errorf("cannot convert %T to int", src)
	}
}

func toFloat(src interface{}) (float64, error) {
	if src == nil {
		return 0, nil
	}
	switch v := src.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case string:
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		return f, err
	default:
		return 0, fmt.Errorf("cannot convert %T to float", src)
	}
}

func toBool(src interface{}) (bool, error) {
	if src == nil {
		return false, nil
	}
	switch v := src.(type) {
	case bool:
		return v, nil
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		return lower == "true" || lower == "1" || lower == "yes", nil
	default:
		return false, fmt.Errorf("cannot convert %T to bool", src)
	}
}

// ============================================================================
// TypedConfig - Alternative map-based access
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
func (c *TypedConfig) Get(name string) interface{} {
	if c.config == nil {
		return nil
	}
	return c.config[name]
}

// String returns a resolved string field
func (c *TypedConfig) String(name string) string {
	if c.config == nil {
		return ""
	}
	v := c.config[name]
	if v == nil {
		return ""
	}
	s, _ := toString(v)
	// Auto-resolve if it's an expression
	if strings.Contains(s, "{{") {
		s = c.resolver.ResolveString(s)
	}
	return s
}

// StringOr returns a resolved string field with default
func (c *TypedConfig) StringOr(name, def string) string {
	s := c.String(name)
	if s == "" {
		return def
	}
	return s
}

// Int returns a resolved int field
func (c *TypedConfig) Int(name string) (int, error) {
	if c.config == nil {
		return 0, fmt.Errorf("config is nil")
	}
	i, err := toInt(c.config[name])
	return int(i), err
}

// IntOr returns a resolved int field with default
func (c *TypedConfig) IntOr(name string, def int) int {
	i, err := c.Int(name)
	if err != nil {
		return def
	}
	return i
}

// Bool returns a resolved bool field
func (c *TypedConfig) Bool(name string) (bool, error) {
	if c.config == nil {
		return false, fmt.Errorf("config is nil")
	}
	return toBool(c.config[name])
}

// BoolOr returns a resolved bool field with default
func (c *TypedConfig) BoolOr(name string, def bool) bool {
	b, err := c.Bool(name)
	if err != nil {
		return def
	}
	return b
}

// Map returns a resolved map field
func (c *TypedConfig) Map(name string) (map[string]interface{}, error) {
	if c.config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	v := c.config[name]
	if v == nil {
		return nil, fmt.Errorf("field %s is nil", name)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("field %s is not a map", name)
	}
	return c.resolver.ResolveMap(m), nil
}

// Slice returns a resolved slice field
func (c *TypedConfig) Slice(name string) ([]interface{}, error) {
	if c.config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	v := c.config[name]
	if v == nil {
		return nil, fmt.Errorf("field %s is nil", name)
	}
	s, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("field %s is not a slice", name)
	}
	return s, nil
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