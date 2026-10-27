package grpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/axiom-studio/skills.sdk/config"
	"github.com/axiom-studio/skills.sdk/executor"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
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

func TestHealthReportsConfigStateAndCheck(t *testing.T) {
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
	if cfg == nil || cfg.ConfigState != "pending" || cfg.ConfigCheck != nil {
		t.Fatalf("config status = %v", cfg)
	}
	var schema config.Schema
	if err := json.Unmarshal(cfg.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Scopes) != 2 || len(schema.Fields) != 2 || schema.Fields[1].Type != config.TypeSecret {
		t.Fatalf("schema = %s", cfg.Schema)
	}

	resp, err = server.Health(context.Background(), &skillpb.HealthRequest{ConfigCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	check := resp.GetConfig().GetConfigCheck()
	if check.GetStatus() != "ok" || len(check.GetChecks()) != 1 || check.Checks[0].Label != "Provider yahoo" {
		t.Fatalf("check = %v", check)
	}
}

func TestHealthWithoutConfig(t *testing.T) {
	resp, err := NewSkillServer("s", "1").Health(context.Background(), &skillpb.HealthRequest{ConfigCheck: true})
	if err != nil || resp.Config != nil || !resp.Healthy {
		t.Fatalf("resp = %v err = %v", resp, err)
	}
}

func TestExecuteScopesConfigToContextTenant(t *testing.T) {
	capture := &configCaptureExecutor{}
	server := NewSkillServer("skill-search", "1.0.0", WithConfig[providerConfig](config.Scopes(config.Global, config.Tenant)))
	server.RegisterExecutor(capture.Type(), capture)
	store, ok := server.config.(*config.Store[providerConfig])
	if !ok {
		t.Fatalf("config runtime = %T", server.config)
	}
	recorder := &bindRecorder{Runtime: store}
	server.config = recorder

	// Model-controlled config/input naming a tenant has no effect.
	tenantArg, _ := json.Marshal("2")
	resp, err := server.Execute(context.Background(), &skillpb.ExecuteRequest{
		NodeType: capture.Type(),
		Config:   map[string][]byte{"tenantId": tenantArg},
		Input:    map[string][]byte{"tenantId": tenantArg},
		Context:  &skillpb.ExecutionContext{TenantId: "1", Variables: map[string]string{"tenantId": "2"}},
	})
	if err != nil || resp.Error != nil {
		t.Fatalf("execute: %v %v", resp, err)
	}
	if capture.seen.Provider != "yahoo" {
		t.Fatalf("config = %+v", capture.seen)
	}
	if len(recorder.tenants) != 1 || recorder.tenants[0] != "1" {
		t.Fatalf("bound tenants = %v, want [1]", recorder.tenants)
	}
}

type bindRecorder struct {
	config.Runtime
	tenants []string
}

func (r *bindRecorder) Bind(ctx context.Context, tenantID string) context.Context {
	r.tenants = append(r.tenants, tenantID)
	return r.Runtime.Bind(ctx, tenantID)
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
