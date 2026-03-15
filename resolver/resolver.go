// Package resolver provides template resolution for Axiom skills.
package resolver

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Config holds the data needed for template resolution
type Config struct {
	Trigger    map[string]interface{}
	Bindings   map[string]interface{}
	Prev       map[string]interface{}
	Nodes      map[string]interface{}
	Variables  map[string]interface{}
	Self       map[string]interface{}
	Run        map[string]interface{}
	ElapsedMs  int64
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
	resolved := r.ResolveString(condition)

	if strings.Contains(resolved, "==") {
		parts := strings.SplitN(resolved, "==", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0]) == strings.TrimSpace(parts[1])
		}
	}

	if strings.Contains(resolved, "!=") {
		parts := strings.SplitN(resolved, "!=", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0]) != strings.TrimSpace(parts[1])
		}
	}

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

// Binding marks a string field as a binding reference.
type Binding string

// Expr marks a string field as an expression that should always be resolved.
type Expr string

// ResolveConfig resolves a config map into a typed struct.
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

		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			key = field.Name
		}
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		srcVal, exists := src[key]
		if !exists {
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
	switch fieldType.Kind() {
	case reflect.String:
		s, err := toString(src)
		if err != nil {
			return err
		}
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
		field.Set(reflect.ValueOf(r.ResolveMap(m)))

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
// Schema Generation
// ============================================================================

// FieldSchema describes a single field in the config schema
type FieldSchema struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Items       *FieldSchema `json:"items,omitempty"`
	Properties  map[string]*FieldSchema `json:"properties,omitempty"`
}

// NodeSchema describes the full schema for a node type
type NodeSchema struct {
	NodeType    string                  `json:"nodeType"`
	Description string                  `json:"description,omitempty"`
	Fields      map[string]*FieldSchema `json:"fields"`
}

// GenerateSchema generates a NodeSchema from a config struct
func GenerateSchema(nodeType string, configType interface{}) *NodeSchema {
	t := reflect.TypeOf(configType)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	schema := &NodeSchema{
		NodeType: nodeType,
		Fields:   make(map[string]*FieldSchema),
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			continue
		}
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		fieldSchema := &FieldSchema{
			Name:        key,
			Type:        goTypeToSchemaType(field.Type),
			Description: field.Tag.Get("description"),
		}

		// Check for required (no default means required)
		def := field.Tag.Get("default")
		if def != "" {
			fieldSchema.Default = parseDefault(def, field.Type)
			fieldSchema.Required = false
		} else {
			fieldSchema.Required = true
		}

		// Handle slice items
		if field.Type.Kind() == reflect.Slice {
			fieldSchema.Items = &FieldSchema{
				Type: goTypeToSchemaType(field.Type.Elem()),
			}
		}

		// Handle nested structs
		if field.Type.Kind() == reflect.Struct {
			fieldSchema.Properties = generateNestedProperties(field.Type)
		}

		schema.Fields[key] = fieldSchema
	}

	return schema
}

func goTypeToSchemaType(t reflect.Type) string {
	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// Handle custom types (Binding, Expr)
	switch t.Name() {
	case "Binding":
		return "binding"
	case "Expr":
		return "expression"
	}

	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "integer"
	case reflect.Float64, reflect.Float32:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map:
		return "object"
	case reflect.Struct:
		return "object"
	default:
		return "string"
	}
}

func parseDefault(def string, t reflect.Type) interface{} {
	switch t.Kind() {
	case reflect.String:
		return def
	case reflect.Int, reflect.Int64, reflect.Int32:
		var i int64
		fmt.Sscanf(def, "%d", &i)
		return i
	case reflect.Float64, reflect.Float32:
		var f float64
		fmt.Sscanf(def, "%f", &f)
		return f
	case reflect.Bool:
		return strings.ToLower(def) == "true"
	default:
		return def
	}
}

func generateNestedProperties(t reflect.Type) map[string]*FieldSchema {
	props := make(map[string]*FieldSchema)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		key := field.Tag.Get("json")
		if key == "" || key == "-" {
			key = field.Name
		}
		if idx := strings.Index(key, ","); idx != -1 {
			key = key[:idx]
		}

		props[key] = &FieldSchema{
			Name:        key,
			Type:        goTypeToSchemaType(field.Type),
			Description: field.Tag.Get("description"),
		}
	}

	return props
}

// ToJSON returns the schema as JSON
func (s *NodeSchema) ToJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// ============================================================================
// TypedConfig - Alternative map-based access
// ============================================================================

type TypedConfig struct {
	config   map[string]interface{}
	resolver *Resolver
}

func NewTypedConfig(config map[string]interface{}, resolver *Resolver) *TypedConfig {
	return &TypedConfig{config: config, resolver: resolver}
}

func (c *TypedConfig) Get(name string) interface{} {
	if c.config == nil {
		return nil
	}
	return c.config[name]
}

func (c *TypedConfig) String(name string) string {
	if c.config == nil {
		return ""
	}
	v := c.config[name]
	if v == nil {
		return ""
	}
	s, _ := toString(v)
	if strings.Contains(s, "{{") {
		s = c.resolver.ResolveString(s)
	}
	return s
}

func (c *TypedConfig) StringOr(name, def string) string {
	s := c.String(name)
	if s == "" {
		return def
	}
	return s
}

func (c *TypedConfig) Int(name string) (int, error) {
	if c.config == nil {
		return 0, fmt.Errorf("config is nil")
	}
	i, err := toInt(c.config[name])
	return int(i), err
}

func (c *TypedConfig) IntOr(name string, def int) int {
	i, err := c.Int(name)
	if err != nil {
		return def
	}
	return i
}

func (c *TypedConfig) Bool(name string) (bool, error) {
	if c.config == nil {
		return false, fmt.Errorf("config is nil")
	}
	return toBool(c.config[name])
}

func (c *TypedConfig) BoolOr(name string, def bool) bool {
	b, err := c.Bool(name)
	if err != nil {
		return def
	}
	return b
}

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

func (c *TypedConfig) Binding(name string) interface{} {
	return c.resolver.GetBinding(name)
}

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

func (c *TypedConfig) Raw() map[string]interface{} {
	return c.config
}