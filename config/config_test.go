package config

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func rawMap(t *testing.T, v map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, val := range v {
		data, err := json.Marshal(val)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = data
	}
	return out
}

func TestFromPrecedenceAndTenantIsolation(t *testing.T) {
	store, err := NewStore[searchConfig]("skill-browser", nil, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Pending: declared defaults; host fields never present.
	cfg := From[searchConfig](store.Bind(ctx, "1"))
	if cfg.SearchProvider != "yahoo" || cfg.MaxResults != 10 || cfg.Region != "us" ||
		cfg.Endpoint != "https://api.example.com" || cfg.OAuthClientID != "" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if store.Status().State != StatePending {
		t.Fatalf("state = %s", store.Status().State)
	}

	l := &loader[searchConfig]{store: store, secrets: &secretsDoc{}}
	l.doc = &document{
		Version: 3,
		Global: rawMap(t, map[string]any{
			"searchProvider": "tavily", "maxResults": 20, "region": "de", // region is tenant-only
			"oauthClientId": "leak", "tavilyApiKey": "not-from-kv",
		}),
		Tenants: map[string]map[string]json.RawMessage{
			"1": rawMap(t, map[string]any{"searchProvider": "yahoo", "region": "fr", "endpoint": "https://t1",
				"maxResults": 999}), // endpoint is global-only; 999 is out of range
			"2": rawMap(t, map[string]any{"fallbackToYahoo": true}),
		},
	}
	l.secretsFetched = &secretsVersion{Global: 1, Tenants: map[string]uint64{"2": 1}}
	l.doc.SecretsVersion = *l.secretsFetched
	l.secrets = &secretsDoc{
		Global:  map[string]string{"tavilyApiKey": "global-key", "oauthClientSecret": "host"},
		Tenants: map[string]map[string]string{"2": {"tavilyApiKey": "tenant-2-key"}},
	}
	if err := l.apply(ctx); err != nil {
		t.Fatal(err)
	}
	if st := store.Status(); st.State != StateLoaded || st.Version != 3 || !strings.Contains(st.Detail, "maxResults") {
		t.Fatalf("status = %+v", st)
	}

	t1 := From[searchConfig](store.Bind(ctx, "1"))
	t2 := From[searchConfig](store.Bind(ctx, "2"))
	t3 := From[searchConfig](store.Bind(ctx, "3"))
	none := From[searchConfig](store.Bind(ctx, ""))

	if t1.SearchProvider != "yahoo" || t1.Region != "fr" || t1.Endpoint != "https://api.example.com" ||
		t1.MaxResults != 20 || t1.TavilyAPIKey.Reveal() != "global-key" {
		t.Fatalf("tenant 1 = %+v (%s)", t1, t1.TavilyAPIKey.Reveal())
	}
	if t2.SearchProvider != "tavily" || !t2.FallbackToYahoo || t2.Region != "us" ||
		t2.TavilyAPIKey.Reveal() != "tenant-2-key" {
		t.Fatalf("tenant 2 = %+v", t2)
	}
	for name, c := range map[string]searchConfig{"tenant 3": t3, "no tenant": none} {
		if c.SearchProvider != "tavily" || c.FallbackToYahoo || c.Region != "us" ||
			c.TavilyAPIKey.Reveal() != "global-key" {
			t.Fatalf("%s = %+v", name, c)
		}
	}
	for _, c := range []searchConfig{t1, t2, t3, none} {
		if c.OAuthClientID != "" || c.OAuthSecret.IsSet() {
			t.Fatalf("host field delivered: %+v", c)
		}
	}
}

func TestFromWithoutServerReturnsDefaults(t *testing.T) {
	cfg := From[searchConfig](context.Background())
	if cfg.SearchProvider != "yahoo" || cfg.MaxResults != 10 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestFromWrongTypePanics(t *testing.T) {
	store, err := NewStore[searchConfig]("s", nil, Scopes(Global, Tenant))
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
	From[other](store.Bind(context.Background(), "1"))
}

func TestCheckDerivesStatus(t *testing.T) {
	store, err := NewStore[searchConfig]("s", func(ctx context.Context, c searchConfig) CheckResult {
		if From[searchConfig](ctx).SearchProvider != c.SearchProvider {
			panic("ctx not bound")
		}
		return CheckResult{Checks: []Check{{ID: "a", Label: "A", Status: CheckOK}, {ID: "b", Label: "B", Status: CheckWarn}}}
	}, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := store.Check(context.Background(), "1")
	if !ok || res.Status != CheckWarn {
		t.Fatalf("check = %+v %v", res, ok)
	}
}
