package grpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/config"
	"github.com/axiom-studio/skills.sdk/executor"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"google.golang.org/protobuf/types/known/structpb"
)

type providerConfig struct {
	Provider string        `config:"provider" label:"Provider" enum:"yahoo,tavily" default:"yahoo"`
	APIKey   config.Secret `config:"apiKey" label:"API key" requiredWhen:"provider=tavily"`
}

type configCaptureExecutor struct{ seen providerConfig }

func (*configCaptureExecutor) Type() string { return "config-capture" }

func (e *configCaptureExecutor) Execute(ctx context.Context, _ *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	e.seen = config.From[providerConfig](ctx)
	return &executor.StepResult{Output: map[string]interface{}{}}, nil
}

func TestHealthReportsSchemaAndRunsCheckOnValues(t *testing.T) {
	server := NewSkillServer("skill-search", "1.0.0",
		WithConfig[providerConfig](config.Scopes(config.Global, config.Tenant)),
		WithConfigCheck(func(_ context.Context, cfg providerConfig) config.CheckResult {
			status := config.CheckOK
			if cfg.Provider == "tavily" && !cfg.APIKey.IsSet() {
				status = config.CheckFail
			}
			return config.CheckResult{Checks: []config.Check{{ID: "provider", Label: "Provider " + cfg.Provider, Status: status}}}
		}),
	)

	resp, err := server.Health(context.Background(), &skillpb.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := resp.GetConfig()
	if cfg == nil || !cfg.HasConfigCheck || cfg.ConfigCheck != nil {
		t.Fatalf("config status = %v", cfg)
	}
	var schema config.Schema
	if err := json.Unmarshal(cfg.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Scopes) != 2 || len(schema.Fields) != 2 || schema.Fields[1].Type != config.TypeSecret {
		t.Fatalf("schema = %s", cfg.Schema)
	}

	check := func(values map[string]any) *skillpb.ConfigCheckResult {
		t.Helper()
		req := &skillpb.HealthRequest{ConfigCheck: true, ConfigTenantId: "1"}
		if values != nil {
			st, err := structpb.NewStruct(values)
			if err != nil {
				t.Fatal(err)
			}
			req.ConfigValues = st
		}
		resp, err := server.Health(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetConfig().GetConfigCheck()
	}
	if c := check(nil); c.GetStatus() != "ok" || len(c.GetChecks()) != 1 || c.Checks[0].Label != "Provider yahoo" {
		t.Fatalf("defaults check = %v", c)
	}
	if c := check(map[string]any{"provider": "tavily"}); c.GetStatus() != "fail" || c.Checks[0].Label != "Provider tavily" {
		t.Fatalf("values check = %v", c)
	}
	if c := check(map[string]any{"provider": "tavily", "apiKey": "k"}); c.GetStatus() != "ok" {
		t.Fatalf("values check = %v", c)
	}
	if c := check(map[string]any{"provider": 1}); c.GetStatus() != "fail" || c.Checks[0].Id != "config" {
		t.Fatalf("invalid values check = %v", c)
	}

	noCheck := NewSkillServer("s", "1", WithConfig[providerConfig]())
	resp, _ = noCheck.Health(context.Background(), &skillpb.HealthRequest{ConfigCheck: true})
	if resp.GetConfig().GetHasConfigCheck() || resp.GetConfig().GetConfigCheck() != nil {
		t.Fatalf("no-check status = %v", resp.GetConfig())
	}
}

func TestHealthWithoutConfig(t *testing.T) {
	resp, err := NewSkillServer("s", "1").Health(context.Background(), &skillpb.HealthRequest{ConfigCheck: true})
	if err != nil || resp.Config != nil || !resp.Healthy {
		t.Fatalf("resp = %v err = %v", resp, err)
	}
}

func TestExecuteReadsConfigOnlyFromExecutionContext(t *testing.T) {
	capture := &configCaptureExecutor{}
	server := NewSkillServer("skill-search", "1.0.0", WithConfig[providerConfig](config.Scopes(config.Global, config.Tenant)))
	server.RegisterExecutor(capture.Type(), capture)

	execute := func(ec *skillpb.ExecutionContext) *skillpb.ExecuteResponse {
		t.Helper()
		// Model-controlled config/input/variables naming config have no effect.
		arg, _ := json.Marshal("tavily")
		resp, err := server.Execute(context.Background(), &skillpb.ExecuteRequest{
			NodeType: capture.Type(),
			Config:   map[string][]byte{"provider": arg},
			Input:    map[string][]byte{"provider": arg},
			Context:  ec,
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := execute(&skillpb.ExecutionContext{TenantId: "1", Variables: map[string]string{"provider": "tavily"}}); resp.Error != nil ||
		capture.seen.Provider != "yahoo" {
		t.Fatalf("absent config: %v %+v", resp.Error, capture.seen)
	}

	t1, _ := structpb.NewStruct(map[string]any{"provider": "tavily", "apiKey": "tenant-1-key"})
	if resp := execute(&skillpb.ExecutionContext{TenantId: "1", ConfigVersion: "7", Config: t1}); resp.Error != nil ||
		capture.seen.Provider != "tavily" || capture.seen.APIKey.Reveal() != "tenant-1-key" {
		t.Fatalf("tenant 1: %v %+v", resp.Error, capture.seen)
	}
	// Tenant 2 on the same version sees only its own config.
	if resp := execute(&skillpb.ExecutionContext{TenantId: "2", ConfigVersion: "7"}); resp.Error != nil ||
		capture.seen.Provider != "yahoo" || capture.seen.APIKey.IsSet() {
		t.Fatalf("tenant 2: %v %+v", resp.Error, capture.seen)
	}

	bad, _ := structpb.NewStruct(map[string]any{"provider": "bing", "apiKey": "secret-value"})
	resp := execute(&skillpb.ExecutionContext{TenantId: "3", Config: bad})
	if resp.Error == nil || resp.Error.Type != "config" || !strings.Contains(resp.Error.Message, "provider") ||
		strings.Contains(resp.Error.Message, "secret-value") {
		t.Fatalf("invalid config error = %v", resp.Error)
	}
}

func TestConfigOptionMisuse(t *testing.T) {
	expectPanic := func(name, want string, fn func()) {
		t.Helper()
		defer func() {
			r := recover()
			if r == nil || !strings.Contains(strings.ToLower(fmtAny(r)), want) {
				t.Fatalf("%s: panic = %v, want %q", name, r, want)
			}
		}()
		fn()
	}
	expectPanic("check without config", "requires withconfig", func() {
		NewSkillServer("s", "1", WithConfigCheck(func(context.Context, providerConfig) config.CheckResult { return config.CheckResult{} }))
	})
	expectPanic("check type mismatch", "withconfigcheck", func() {
		type other struct {
			A string `config:"a" label:"A"`
		}
		NewSkillServer("s", "1", WithConfig[providerConfig](),
			WithConfigCheck(func(context.Context, other) config.CheckResult { return config.CheckResult{} }))
	})
	expectPanic("invalid declaration", "unsupported type", func() {
		type bad struct {
			A float64 `config:"a" label:"A"`
		}
		NewSkillServer("s", "1", WithConfig[bad]())
	})
}

func fmtAny(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
