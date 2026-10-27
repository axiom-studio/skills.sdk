// Package config is the Skills SDK's operator-config feature: a Skill declares
// its settings as a tagged struct, the SDK derives the shared field schema from
// it, and From decodes the effective config the platform sends with each call
// (ExecutionContext.config, resolved for the call's tenant) into that struct.
//
//	type Config struct {
//	    SearchProvider  string        `config:"searchProvider" label:"Search provider" enum:"yahoo,tavily" default:"yahoo"`
//	    TavilyAPIKey    config.Secret `config:"tavilyApiKey" label:"Tavily API key" requiredWhen:"searchProvider=tavily"`
//	    FallbackToYahoo bool          `config:"fallbackToYahoo" label:"Fall back to Yahoo"`
//	}
//
// Struct tags: config (key, required), label (required), description, type
// ("url" on a string field; other types follow the Go type), enum
// (comma-separated), default, required, requiredWhen ("field=value", several
// joined by commas), scopes ("global,tenant"), audience ("skill"|"host"),
// summary, min, max, pattern.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/structpb"
)

// Scope is where an operator may set a value.
type Scope string

const (
	Global Scope = "global"
	Tenant Scope = "tenant"
)

// FieldType is a config field's type in the shared schema.
type FieldType string

const (
	TypeString FieldType = "string"
	TypeEnum   FieldType = "enum"
	TypeBool   FieldType = "bool"
	TypeInt    FieldType = "int"
	TypeSecret FieldType = "secret"
	TypeURL    FieldType = "url"
)

// Audience says who receives a field's value.
type Audience string

const (
	// AudienceSkill fields are delivered to the Skill's pods (the default).
	AudienceSkill Audience = "skill"
	// AudienceHost fields are used by the platform on the Skill's behalf and
	// are never delivered to the Skill.
	AudienceHost Audience = "host"
)

// Field is one entry of config.fields[] in the shared schema. Optional
// properties at their default value are omitted.
type Field struct {
	Key          string         `json:"key"`
	Label        string         `json:"label"`
	Description  string         `json:"description,omitempty"`
	Type         FieldType      `json:"type"`
	Enum         []string       `json:"enum,omitempty"`
	Default      any            `json:"default,omitempty"`
	Required     bool           `json:"required,omitempty"`
	RequiredWhen map[string]any `json:"requiredWhen,omitempty"`
	Scopes       []Scope        `json:"scopes,omitempty"`
	Audience     Audience       `json:"audience,omitempty"`
	Summary      bool           `json:"summary,omitempty"`
	Min          *int64         `json:"min,omitempty"`
	Max          *int64         `json:"max,omitempty"`
	Pattern      string         `json:"pattern,omitempty"`
}

// Schema is the config section a Skill declares.
type Schema struct {
	Scopes []Scope `json:"scopes"`
	Fields []Field `json:"fields"`
}

// JSON returns the schema in its wire form.
func (s *Schema) JSON() []byte {
	data, _ := json.Marshal(s)
	return data
}

// Option configures a config declaration.
type Option func(*options)

type options struct {
	scopes []Scope
}

// Scopes sets where an operator may set values. The default is Global only.
func Scopes(scopes ...Scope) Option {
	return func(o *options) { o.scopes = scopes }
}

var (
	keyPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,63}$`)
	secretType = reflect.TypeFor[Secret]()
	knownTags  = []string{"config", "label", "description", "type", "enum", "default", "required",
		"requiredWhen", "scopes", "audience", "summary", "min", "max", "pattern"}
)

// field is a declared field plus what the SDK needs to read and set it.
type field struct {
	Field
	index      []int
	re         *regexp.Regexp
	goType     reflect.Type
	effectiveV any         // parsed default, nil when none
	when       []condition // RequiredWhen in declaration order
}

// condition is one requiredWhen condition: the field key must equal value.
type condition struct {
	key   string
	value any
}

type declaration struct {
	typ    reflect.Type
	schema *Schema
	fields []*field
}

// SchemaOf derives and validates the config schema of T. It returns an error
// for any invalid declaration.
func SchemaOf[T any](opts ...Option) (*Schema, error) {
	d, err := declare(reflect.TypeFor[T](), opts)
	if err != nil {
		return nil, err
	}
	return d.schema, nil
}

func declare(t reflect.Type, opts []Option) (*declaration, error) {
	o := options{scopes: []Scope{Global}}
	for _, opt := range opts {
		opt(&o)
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config: %s is not a struct", t)
	}
	if len(o.scopes) == 0 {
		return nil, errors.New("config: scopes must not be empty")
	}
	sectionScopes, err := normalizeScopes(o.scopes)
	if err != nil {
		return nil, err
	}

	d := &declaration{typ: t, schema: &Schema{Scopes: sectionScopes, Fields: []Field{}}}
	byKey := map[string]*field{}
	whenRaw := map[*field]string{}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		key, tagged := sf.Tag.Lookup("config")
		if key == "-" {
			continue
		}
		if !tagged {
			return nil, fmt.Errorf("config: field %s has no config tag (use config:\"-\" to skip it)", sf.Name)
		}
		if !sf.IsExported() {
			return nil, fmt.Errorf("config: field %s must be exported", sf.Name)
		}
		f, when, err := declareField(sf, key, sectionScopes)
		if err != nil {
			return nil, fmt.Errorf("config: field %s (%q): %w", sf.Name, key, err)
		}
		if byKey[key] != nil {
			return nil, fmt.Errorf("config: duplicate key %q", key)
		}
		byKey[key] = f
		whenRaw[f] = when
		d.fields = append(d.fields, f)
	}
	if len(d.fields) == 0 {
		return nil, fmt.Errorf("config: %s declares no fields", t)
	}
	for _, f := range d.fields {
		if whenRaw[f] == "" {
			continue
		}
		when := map[string]any{}
		for _, cond := range strings.Split(whenRaw[f], ",") {
			name, value, ok := strings.Cut(strings.TrimSpace(cond), "=")
			if !ok {
				return nil, fmt.Errorf("config: field %q: requiredWhen %q must be field=value", f.Key, cond)
			}
			ref := byKey[name]
			if ref == nil {
				return nil, fmt.Errorf("config: field %q: requiredWhen references unknown field %q", f.Key, name)
			}
			if ref == f {
				return nil, fmt.Errorf("config: field %q: requiredWhen references itself", f.Key)
			}
			if ref.Type == TypeSecret {
				return nil, fmt.Errorf("config: field %q: requiredWhen cannot reference secret %q", f.Key, name)
			}
			v, err := ref.parseText(value)
			if err != nil {
				return nil, fmt.Errorf("config: field %q: requiredWhen %s: %w", f.Key, name, err)
			}
			when[name] = v
			f.when = append(f.when, condition{name, v})
		}
		f.RequiredWhen = when
	}
	for _, f := range d.fields {
		d.schema.Fields = append(d.schema.Fields, f.Field)
	}
	return d, nil
}

func declareField(sf reflect.StructField, key string, section []Scope) (*field, string, error) {
	tag := sf.Tag
	f := &field{index: sf.Index, goType: sf.Type}
	f.Key = key
	if !keyPattern.MatchString(key) {
		return nil, "", fmt.Errorf("key must match %s", keyPattern)
	}
	f.Label = tag.Get("label")
	if f.Label == "" {
		return nil, "", errors.New("label is required")
	}
	if utf8.RuneCountInString(f.Label) > 60 {
		return nil, "", errors.New("label exceeds 60 characters")
	}
	f.Description = tag.Get("description")
	if utf8.RuneCountInString(f.Description) > 200 {
		return nil, "", errors.New("description exceeds 200 characters")
	}

	enumTag, hasEnum := tag.Lookup("enum")
	typeTag := tag.Get("type")
	switch {
	case sf.Type == secretType:
		f.Type = TypeSecret
	case sf.Type.Kind() == reflect.String:
		f.Type = TypeString
		if hasEnum {
			f.Type = TypeEnum
		}
	case sf.Type.Kind() == reflect.Bool:
		f.Type = TypeBool
	case slices.Contains([]reflect.Kind{reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64}, sf.Type.Kind()):
		f.Type = TypeInt
	default:
		return nil, "", fmt.Errorf("unsupported type %s (want string, bool, int or config.Secret)", sf.Type)
	}
	if typeTag != "" {
		if typeTag != string(f.Type) && !(typeTag == string(TypeURL) && f.Type == TypeString) {
			return nil, "", fmt.Errorf("type %q does not fit Go type %s", typeTag, sf.Type)
		}
		f.Type = FieldType(typeTag)
	}

	if hasEnum {
		if f.Type != TypeEnum {
			return nil, "", errors.New("enum is only allowed on string fields")
		}
		for _, v := range strings.Split(enumTag, ",") {
			v = strings.TrimSpace(v)
			if v == "" || slices.Contains(f.Enum, v) {
				return nil, "", fmt.Errorf("enum %q has an empty or duplicate value", enumTag)
			}
			f.Enum = append(f.Enum, v)
		}
	}

	var err error
	if f.Required, err = boolTag(tag, "required"); err != nil {
		return nil, "", err
	}
	if f.Summary, err = boolTag(tag, "summary"); err != nil {
		return nil, "", err
	}
	switch a := tag.Get("audience"); a {
	case "", string(AudienceSkill):
	case string(AudienceHost):
		f.Audience = AudienceHost
	default:
		return nil, "", fmt.Errorf("audience %q must be skill or host", a)
	}
	if s, ok := tag.Lookup("scopes"); ok {
		var scopes []Scope
		for _, v := range strings.Split(s, ",") {
			scopes = append(scopes, Scope(strings.TrimSpace(v)))
		}
		if f.Scopes, err = normalizeScopes(scopes); err != nil {
			return nil, "", err
		}
		for _, sc := range f.Scopes {
			if !slices.Contains(section, sc) {
				return nil, "", fmt.Errorf("scope %q is not in the section scopes %v", sc, section)
			}
		}
		if slices.Equal(f.Scopes, section) {
			f.Scopes = nil // the default
		}
	}
	scopes := f.Scopes
	if scopes == nil {
		scopes = section
	}

	for _, name := range []string{"min", "max"} {
		s, ok := tag.Lookup(name)
		if !ok {
			continue
		}
		if f.Type != TypeInt {
			return nil, "", fmt.Errorf("%s is only allowed on int fields", name)
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("%s %q is not an integer", name, s)
		}
		if name == "min" {
			f.Min = &n
		} else {
			f.Max = &n
		}
	}
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		return nil, "", errors.New("min exceeds max")
	}
	if p, ok := tag.Lookup("pattern"); ok {
		if f.Type != TypeString {
			return nil, "", errors.New("pattern is only allowed on string fields")
		}
		if f.re, err = regexp.Compile(p); err != nil {
			return nil, "", fmt.Errorf("pattern: %w", err)
		}
		f.Pattern = p
	}
	if d, ok := tag.Lookup("default"); ok {
		if f.Type == TypeSecret {
			return nil, "", errors.New("secret fields cannot have a default")
		}
		v, err := f.parseText(d)
		if err != nil {
			return nil, "", fmt.Errorf("default: %w", err)
		}
		f.Default = v
		f.effectiveV = v
	}
	for name := range tagNames(tag) {
		if !slices.Contains(knownTags, name) && name != "json" && name != "yaml" {
			return nil, "", fmt.Errorf("unknown tag %q", name)
		}
	}
	return f, tag.Get("requiredWhen"), nil
}

func boolTag(tag reflect.StructTag, name string) (bool, error) {
	s, ok := tag.Lookup(name)
	if !ok {
		return false, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("%s %q is not a bool", name, s)
	}
	return b, nil
}

// tagNames yields the keys of a conventional struct tag.
func tagNames(tag reflect.StructTag) map[string]struct{} {
	names := map[string]struct{}{}
	s := string(tag)
	for s != "" {
		s = strings.TrimLeft(s, " ")
		i := strings.Index(s, ":\"")
		if i <= 0 {
			break
		}
		names[s[:i]] = struct{}{}
		s = s[i+2:]
		for j := 0; j < len(s); j++ {
			if s[j] == '\\' {
				j++
				continue
			}
			if s[j] == '"' {
				s = s[j+1:]
				break
			}
		}
	}
	return names
}

func normalizeScopes(in []Scope) ([]Scope, error) {
	if len(in) == 0 {
		return nil, errors.New("scopes must not be empty")
	}
	var out []Scope
	for _, s := range []Scope{Global, Tenant} {
		if slices.Contains(in, s) {
			out = append(out, s)
		}
	}
	for _, s := range in {
		if s != Global && s != Tenant {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
	}
	return out, nil
}

// parseText parses a tag value (default, requiredWhen) into the field's type.
func (f *field) parseText(s string) (any, error) {
	var v any
	switch f.Type {
	case TypeBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not a bool", s)
		}
		v = b
	case TypeInt:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an int", s)
		}
		v = n
	case TypeSecret:
		return nil, errors.New("secret values cannot appear in declarations")
	default:
		v = s
	}
	return v, f.check(v)
}

// decode converts a value the platform delivered into the field's type. Errors
// name the key and the expected type but never echo a secret's value.
func (f *field) decode(pv *structpb.Value) (any, error) {
	var v any
	switch f.Type {
	case TypeBool:
		b, ok := pv.GetKind().(*structpb.Value_BoolValue)
		if !ok {
			return nil, fmt.Errorf("%s: not a %s", f.Key, f.Type)
		}
		v = b.BoolValue
	case TypeInt:
		_, ok := pv.GetKind().(*structpb.Value_NumberValue)
		x := pv.GetNumberValue()
		if !ok || x != math.Trunc(x) || x < math.MinInt64 || x >= math.MaxInt64 ||
			reflect.Zero(f.goType).OverflowInt(int64(x)) {
			return nil, fmt.Errorf("%s: not a %s", f.Key, f.Type)
		}
		v = int64(x)
	default:
		sv, ok := pv.GetKind().(*structpb.Value_StringValue)
		if !ok {
			return nil, fmt.Errorf("%s: not a %s", f.Key, f.Type)
		}
		v = sv.StringValue
	}
	if err := f.check(v); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Key, err)
	}
	return v, nil
}

// check validates a typed value against the field's constraints.
func (f *field) check(v any) error {
	switch f.Type {
	case TypeEnum:
		if !slices.Contains(f.Enum, v.(string)) {
			return fmt.Errorf("%q is not one of %v", v, f.Enum)
		}
	case TypeInt:
		n := v.(int64)
		if f.Min != nil && n < *f.Min {
			return fmt.Errorf("%d is below min %d", n, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return fmt.Errorf("%d is above max %d", n, *f.Max)
		}
	case TypeString:
		if f.re != nil && !f.re.MatchString(v.(string)) {
			return fmt.Errorf("value does not match pattern %s", f.Pattern)
		}
	}
	return nil
}

// set stores a typed value into the struct field.
func (f *field) set(dst reflect.Value, v any) {
	fv := dst.FieldByIndex(f.index)
	switch x := v.(type) {
	case bool:
		fv.SetBool(x)
	case int64:
		fv.SetInt(x)
	case string:
		if f.Type == TypeSecret {
			fv.Set(reflect.ValueOf(NewSecret(x)))
		} else {
			fv.SetString(x)
		}
	}
}

// decodeInto writes declared defaults overlaid with values into dst. Missing
// and null keys keep their default, unknown keys are ignored, and host-audience
// fields are never set. Every invalid value is reported. With checkRequired,
// a required field, or a requiredWhen field whose conditions all hold, that is
// still unset after defaults is reported too (only when no value was invalid).
func (d *declaration) decodeInto(dst reflect.Value, values *structpb.Struct, checkRequired bool) error {
	var errs []error
	effective := make(map[string]any, len(d.fields))
	for _, f := range d.fields {
		v := f.effectiveV
		if f.Audience != AudienceHost {
			if pv, ok := values.GetFields()[f.Key]; ok {
				if _, null := pv.GetKind().(*structpb.Value_NullValue); !null && pv.GetKind() != nil {
					parsed, err := f.decode(pv)
					if err != nil {
						errs = append(errs, err)
						continue
					}
					v = parsed
				}
			}
			if v != nil {
				f.set(dst, v)
			}
		}
		effective[f.Key] = f.zeroed(v)
	}
	if !checkRequired || len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, f := range d.fields {
		if f.Audience == AudienceHost || effective[f.Key] != "" {
			continue
		}
		if f.Required {
			errs = append(errs, fmt.Errorf("%s: required", f.Key))
			continue
		}
		if len(f.when) == 0 {
			continue
		}
		conds := make([]string, 0, len(f.when))
		holds := true
		for _, c := range f.when {
			if effective[c.key] != c.value {
				holds = false
				break
			}
			conds = append(conds, fmt.Sprintf("%s=%v", c.key, c.value))
		}
		if holds {
			errs = append(errs, fmt.Errorf("%s: required when %s", f.Key, strings.Join(conds, ", ")))
		}
	}
	return errors.Join(errs...)
}

// zeroed is v, or the field type's zero value when v is nil. Only string-like
// fields (string, enum, url, secret) can be empty: a bool or int always has a
// value.
func (f *field) zeroed(v any) any {
	if v != nil {
		return v
	}
	switch f.Type {
	case TypeBool:
		return false
	case TypeInt:
		return int64(0)
	}
	return ""
}

var declarations sync.Map // reflect.Type -> *declaration (default options)

// defaultsOf returns T's declared defaults, for calls without a configured
// server (unit tests of executors). It panics on an invalid declaration.
func defaultsOf[T any]() T {
	t := reflect.TypeFor[T]()
	cached, ok := declarations.Load(t)
	if !ok {
		d, err := declare(t, []Option{Scopes(Global, Tenant)}) // defaults do not depend on scopes
		if err != nil {
			panic(err)
		}
		cached, _ = declarations.LoadOrStore(t, d)
	}
	var out T
	_ = cached.(*declaration).decodeInto(reflect.ValueOf(&out).Elem(), nil, false)
	return out
}
