package config

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

var update = flag.Bool("update", false, "rewrite golden files")

type searchConfig struct {
	SearchProvider  string `config:"searchProvider" label:"Search provider" description:"Engine behind web search." enum:"yahoo,tavily" default:"yahoo" summary:"true"`
	TavilyAPIKey    Secret `config:"tavilyApiKey" label:"Tavily API key" requiredWhen:"searchProvider=tavily"`
	FallbackToYahoo bool   `config:"fallbackToYahoo" label:"Fall back to Yahoo" default:"false" summary:"true"`
	MaxResults      int    `config:"maxResults" label:"Max results" default:"10" min:"1" max:"50"`
	Region          string `config:"region" label:"Region" pattern:"^[a-z]{2}$" default:"us" scopes:"tenant"`
	Endpoint        string `config:"endpoint" label:"Endpoint" type:"url" scopes:"global" required:"true" default:"https://api.example.com"`
	OAuthSecret     Secret `config:"oauthClientSecret" label:"OAuth client secret" audience:"host" scopes:"global"`
	OAuthClientID   string `config:"oauthClientId" label:"OAuth client ID" audience:"host" default:"host-only"`
	Ignored         string `config:"-"`
}

func TestSchemaGolden(t *testing.T) {
	schema, err := SchemaOf[searchConfig](Scopes(Tenant, Global))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetIndent("", "  ")
	if err := enc.Encode(schema); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "schema.golden.json")
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("schema mismatch (run with -update to accept)\ngot:\n%s\nwant:\n%s", got.Bytes(), want)
	}
	// The wire form is the same JSON.
	if !json.Valid(schema.JSON()) {
		t.Fatal("schema JSON invalid")
	}
}

func TestInvalidDeclarations(t *testing.T) {
	cases := map[string]struct {
		fn   func() error
		want string
	}{
		"unknown type": {func() error {
			_, err := SchemaOf[struct {
				A float64 `config:"a" label:"A"`
			}]()
			return err
		}, "unsupported type"},
		"enum without values": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" enum:""`
			}]()
			return err
		}, "empty or duplicate"},
		"enum on bool": {func() error {
			_, err := SchemaOf[struct {
				A bool `config:"a" label:"A" enum:"x"`
			}]()
			return err
		}, "only allowed on string"},
		"default not matching type": {func() error {
			_, err := SchemaOf[struct {
				A int `config:"a" label:"A" default:"many"`
			}]()
			return err
		}, "not an int"},
		"default outside enum": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" enum:"x,y" default:"z"`
			}]()
			return err
		}, "not one of"},
		"default outside range": {func() error {
			_, err := SchemaOf[struct {
				A int `config:"a" label:"A" min:"1" max:"5" default:"9"`
			}]()
			return err
		}, "above max"},
		"secret with default": {func() error {
			_, err := SchemaOf[struct {
				A Secret `config:"a" label:"A" default:"x"`
			}]()
			return err
		}, "cannot have a default"},
		"requiredWhen unknown field": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" requiredWhen:"b=x"`
			}]()
			return err
		}, "unknown field"},
		"requiredWhen bad value": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" enum:"x,y"`
				B string `config:"b" label:"B" requiredWhen:"a=z"`
			}]()
			return err
		}, "not one of"},
		"missing label": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a"`
			}]()
			return err
		}, "label is required"},
		"bad key": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"Bad_key" label:"A"`
			}]()
			return err
		}, "key must match"},
		"duplicate key": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A"`
				B string `config:"a" label:"B"`
			}]()
			return err
		}, "duplicate key"},
		"untagged field": {func() error {
			_, err := SchemaOf[struct {
				A string
			}]()
			return err
		}, "no config tag"},
		"field scope outside section": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" scopes:"tenant"`
			}](Scopes(Global))
			return err
		}, "not in the section scopes"},
		"min on string": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" min:"1"`
			}]()
			return err
		}, "only allowed on int"},
		"pattern on int": {func() error {
			_, err := SchemaOf[struct {
				A int `config:"a" label:"A" pattern:"x"`
			}]()
			return err
		}, "only allowed on string"},
		"unknown audience": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" audience:"agent"`
			}]()
			return err
		}, "audience"},
		"unknown tag": {func() error {
			_, err := SchemaOf[struct {
				A string `config:"a" label:"A" defualt:"x"`
			}]()
			return err
		}, "unknown tag"},
		"type url on int": {func() error {
			_, err := SchemaOf[struct {
				A int `config:"a" label:"A" type:"url"`
			}]()
			return err
		}, "does not fit"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.fn()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestSecretRedaction(t *testing.T) {
	const value = "tvly-super-secret"
	s := NewSecret(value)
	holder := struct {
		Key    Secret
		hidden Secret
	}{s, s}
	outputs := []string{
		fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s), fmt.Sprintf("%s", s),
		fmt.Sprintf("%q", s), fmt.Sprintf("%x", s), fmt.Sprint(s), s.String(), s.GoString(),
		fmt.Sprintf("%v", holder), fmt.Sprintf("%+v", holder), fmt.Sprintf("%#v", holder),
		fmt.Sprintf("%v", &holder),
	}
	data, err := json.Marshal(holder)
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(data))
	text, _ := s.MarshalText()
	outputs = append(outputs, string(text))
	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	logger.Info("cfg", "key", s, "holder", holder)
	slog.New(slog.NewTextHandler(&logged, nil)).Info("cfg", "key", s)
	outputs = append(outputs, logged.String())
	for _, out := range outputs {
		if strings.Contains(out, value) {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if !strings.Contains(fmt.Sprint(s), "redacted") {
		t.Fatalf("String() = %q", s)
	}
	if s.Reveal() != value || !s.IsSet() {
		t.Fatal("Reveal lost the value")
	}
	if (Secret{}).IsSet() || (Secret{}).Reveal() != "" {
		t.Fatal("zero Secret should be unset")
	}
}

func values(t *testing.T, v map[string]any) *structpb.Struct {
	t.Helper()
	st, err := structpb.NewStruct(v)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func newSpec(t *testing.T) *Spec[searchConfig] {
	t.Helper()
	spec, err := NewSpec[searchConfig](nil, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func bindFrom(t *testing.T, spec *Spec[searchConfig], call Call) searchConfig {
	t.Helper()
	ctx, err := spec.Bind(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	return From[searchConfig](ctx)
}

func TestFromValuesOverDefaults(t *testing.T) {
	spec := newSpec(t)

	// Absent config: declared defaults; host fields never present.
	cfg := bindFrom(t, spec, Call{TenantID: "1"})
	if cfg.SearchProvider != "yahoo" || cfg.MaxResults != 10 || cfg.Region != "us" ||
		cfg.Endpoint != "https://api.example.com" || cfg.OAuthClientID != "" || cfg.TavilyAPIKey.IsSet() {
		t.Fatalf("defaults = %+v", cfg)
	}

	cfg = bindFrom(t, spec, Call{TenantID: "1", Values: values(t, map[string]any{
		"searchProvider": "tavily", "maxResults": 20, "region": nil, // null keeps the default
		"tavilyApiKey": "tvly-key", "fallbackToYahoo": true,
		"oauthClientId": "leak", "oauthClientSecret": "leak", "unknownKey": 1,
	})})
	if cfg.SearchProvider != "tavily" || cfg.MaxResults != 20 || !cfg.FallbackToYahoo ||
		cfg.Region != "us" || cfg.Endpoint != "https://api.example.com" || cfg.TavilyAPIKey.Reveal() != "tvly-key" {
		t.Fatalf("decoded = %+v", cfg)
	}
	if cfg.OAuthClientID != "" || cfg.OAuthSecret.IsSet() {
		t.Fatalf("host field delivered: %+v", cfg)
	}
}

func TestFromTypeErrors(t *testing.T) {
	spec := newSpec(t)
	for name, tc := range map[string]struct {
		values map[string]any
		want   string
	}{
		"bool as string":   {map[string]any{"fallbackToYahoo": "yes"}, "fallbackToYahoo: not a bool"},
		"int as string":    {map[string]any{"maxResults": "10"}, "maxResults: not a int"},
		"fractional int":   {map[string]any{"maxResults": 1.5}, "maxResults: not a int"},
		"int above max":    {map[string]any{"maxResults": 999}, "above max"},
		"enum value":       {map[string]any{"searchProvider": "bing"}, "is not one of"},
		"pattern":          {map[string]any{"region": "usa"}, "does not match pattern"},
		"string as number": {map[string]any{"endpoint": 3}, "endpoint: not a url"},
		"secret as bool":   {map[string]any{"tavilyApiKey": true}, "tavilyApiKey: not a secret"},
		"several": {map[string]any{"maxResults": "x", "fallbackToYahoo": 1},
			"fallbackToYahoo: not a bool\nmaxResults: not a int"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := spec.Bind(context.Background(), Call{TenantID: "1", Values: values(t, tc.values)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}

	type narrow struct {
		Small int8 `config:"small" label:"Small"`
	}
	n, err := NewSpec[narrow](nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Decode(values(t, map[string]any{"small": 300})); err == nil {
		t.Fatal("int8 overflow accepted")
	}
}

func TestSecretValuesAreRedacted(t *testing.T) {
	const key = "tvly-very-secret"
	spec := newSpec(t)
	cfg := bindFrom(t, spec, Call{TenantID: "1", Values: values(t, map[string]any{"tavilyApiKey": key})})
	data, _ := json.Marshal(cfg)
	for _, out := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), string(data)} {
		if strings.Contains(out, key) {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if cfg.TavilyAPIKey.Reveal() != key {
		t.Fatal("Reveal lost the value")
	}
	// A bad secret value is reported without echoing it.
	_, err := spec.Bind(context.Background(), Call{Values: values(t, map[string]any{"tavilyApiKey": []any{key}})})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v", err)
	}
}

func TestFromUsesOnlyTheCallsConfig(t *testing.T) {
	spec := newSpec(t)
	ctx1, err := spec.Bind(context.Background(), Call{TenantID: "1", Values: values(t, map[string]any{
		"searchProvider": "tavily", "tavilyApiKey": "tenant-1-key",
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, err := spec.Bind(context.Background(), Call{TenantID: "2", Values: values(t, map[string]any{"maxResults": 5})})
	if err != nil {
		t.Fatal(err)
	}
	t1, t2 := From[searchConfig](ctx1), From[searchConfig](ctx2)
	if t1.SearchProvider != "tavily" || t1.TavilyAPIKey.Reveal() != "tenant-1-key" || t1.MaxResults != 10 {
		t.Fatalf("tenant 1 = %+v", t1)
	}
	if t2.SearchProvider != "yahoo" || t2.TavilyAPIKey.IsSet() || t2.MaxResults != 5 {
		t.Fatalf("tenant 2 = %+v", t2)
	}
}

func TestFromWithoutServerReturnsDefaults(t *testing.T) {
	cfg := From[searchConfig](context.Background())
	if cfg.SearchProvider != "yahoo" || cfg.MaxResults != 10 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestFromWrongTypePanics(t *testing.T) {
	ctx, err := newSpec(t).Bind(context.Background(), Call{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	type other struct {
		A string `config:"a" label:"A"`
	}
	From[other](ctx)
}

func TestCheckDecodesValues(t *testing.T) {
	spec, err := NewSpec[searchConfig](func(ctx context.Context, c searchConfig) CheckResult {
		if From[searchConfig](ctx) != c {
			panic("ctx not bound")
		}
		status := CheckOK
		if c.SearchProvider == "tavily" && !c.TavilyAPIKey.IsSet() {
			status = CheckFail
		}
		return CheckResult{Checks: []Check{{ID: "a", Label: "A", Status: CheckOK}, {ID: "key", Label: "Key", Status: status}}}
	}, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.HasCheck() || newSpec(t).HasCheck() {
		t.Fatal("HasCheck wrong")
	}
	if res, ok := spec.Check(context.Background(), nil); !ok || res.Status != CheckOK {
		t.Fatalf("defaults check = %+v %v", res, ok)
	}
	res, _ := spec.Check(context.Background(), values(t, map[string]any{"searchProvider": "tavily"}))
	if res.Status != CheckFail {
		t.Fatalf("check = %+v", res)
	}
	res, _ = spec.Check(context.Background(), values(t, map[string]any{"maxResults": "x"}))
	if res.Status != CheckFail || res.Checks[0].ID != "config" {
		t.Fatalf("invalid values check = %+v", res)
	}
}

// countingCall is a call whose maxResults value shows which decode produced
// the config a call sees.
func countingCall(t *testing.T, tenant, version string, max int) Call {
	return Call{TenantID: tenant, Version: version, Values: values(t, map[string]any{"maxResults": max})}
}

func TestDecodeCache(t *testing.T) {
	spec := newSpec(t)
	ctx := context.Background()
	get := func(call Call) searchConfig {
		t.Helper()
		c, err := spec.Bind(ctx, call)
		if err != nil {
			t.Fatal(err)
		}
		return From[searchConfig](c)
	}

	// Miss, then hit: a hit returns the cached value without decoding, so
	// different values under the same key prove the cache served it.
	if got := get(countingCall(t, "1", "v1", 11)); got.MaxResults != 11 {
		t.Fatalf("miss = %d", got.MaxResults)
	}
	if got := get(countingCall(t, "1", "v1", 12)); got.MaxResults != 11 {
		t.Fatalf("hit = %d, want cached 11", got.MaxResults)
	}
	// Same version, other tenant: no collision.
	if got := get(countingCall(t, "2", "v1", 22)); got.MaxResults != 22 {
		t.Fatalf("tenant 2 = %d", got.MaxResults)
	}
	// A new version replaces the tenant's old entry.
	if got := get(countingCall(t, "1", "v2", 13)); got.MaxResults != 13 {
		t.Fatalf("v2 = %d", got.MaxResults)
	}
	spec.mu.Lock()
	_, oldKept := spec.cache[cacheKey{"1", "v1"}]
	n := spec.lru.Len()
	spec.mu.Unlock()
	if oldKept || n != 2 {
		t.Fatalf("old entry kept = %v, entries = %d", oldKept, n)
	}
	// Empty version: always decoded, never cached.
	if got := get(countingCall(t, "1", "", 14)); got.MaxResults != 14 {
		t.Fatalf("unversioned = %d", got.MaxResults)
	}
	if got := get(countingCall(t, "1", "", 15)); got.MaxResults != 15 {
		t.Fatalf("unversioned = %d", got.MaxResults)
	}
	if spec.lru.Len() != 2 {
		t.Fatalf("entries = %d after unversioned calls", spec.lru.Len())
	}
	// Decode errors are not cached.
	if _, err := spec.Bind(ctx, Call{TenantID: "3", Version: "v1", Values: values(t, map[string]any{"maxResults": "x"})}); err == nil {
		t.Fatal("expected error")
	}
	if got := get(countingCall(t, "3", "v1", 33)); got.MaxResults != 33 {
		t.Fatalf("after error = %d", got.MaxResults)
	}
	// Cached secrets stay redacted Secrets.
	c := get(Call{TenantID: "4", Version: "v1", Values: values(t, map[string]any{"tavilyApiKey": "k4"})})
	c2 := get(Call{TenantID: "4", Version: "v1"})
	if c2.TavilyAPIKey.Reveal() != "k4" || fmt.Sprint(c.TavilyAPIKey) != redacted {
		t.Fatalf("cached secret = %v", c2.TavilyAPIKey)
	}
}

func TestDecodeCacheBound(t *testing.T) {
	spec := newSpec(t)
	for i := range CacheSize + 10 {
		if _, err := spec.Bind(context.Background(), countingCall(t, fmt.Sprint("t", i), "v1", 1+i%50)); err != nil {
			t.Fatal(err)
		}
	}
	spec.mu.Lock()
	n, m := spec.lru.Len(), len(spec.cache)
	_, oldest := spec.cache[cacheKey{"t0", "v1"}]
	_, newest := spec.cache[cacheKey{fmt.Sprint("t", CacheSize+9), "v1"}]
	spec.mu.Unlock()
	if n != CacheSize || m != CacheSize || oldest || !newest {
		t.Fatalf("len = %d/%d, oldest kept = %v, newest kept = %v", n, m, oldest, newest)
	}
}

func TestDecodeCacheConcurrent(t *testing.T) {
	spec := newSpec(t)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				tenant := fmt.Sprint("t", (g+i)%300)
				version := fmt.Sprint("v", i%3)
				want := 1 + (g+i)%300%50
				ctx, err := spec.Bind(context.Background(), Call{TenantID: tenant, Version: version,
					Values: values(t, map[string]any{"maxResults": want})})
				if err != nil {
					t.Error(err)
					return
				}
				if got := From[searchConfig](ctx).MaxResults; got != want {
					t.Errorf("tenant %s got %d want %d", tenant, got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	if spec.lru.Len() > CacheSize {
		t.Fatalf("cache grew to %d", spec.lru.Len())
	}
}

func TestNoNATSDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/axiom-studio/skills.sdk/...").CombinedOutput()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	if strings.Contains(string(out), "nats") {
		t.Fatalf("nats dependency found:\n%s", out)
	}
}
