package grpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
)

type contextCaptureExecutor struct {
	context map[string]interface{}
}

func (*contextCaptureExecutor) Type() string { return "context-capture" }

func (e *contextCaptureExecutor) Execute(
	_ context.Context,
	_ *executor.StepDefinition,
	resolver executor.TemplateResolver,
) (*executor.StepResult, error) {
	provider, ok := resolver.(executor.ContextProvider)
	if ok {
		e.context = provider.GetContextData()
	}
	return &executor.StepResult{Output: map[string]interface{}{
		"runId":        resolver.ResolveString("{{run.id}}"),
		"agentId":      resolver.ResolveString("{{run.agentId}}"),
		"selfId":       resolver.ResolveString("{{self.id}}"),
		"namespace":    resolver.ResolveString("{{run.namespace}}"),
		"actionCallId": resolver.ResolveString("{{var.actionCallId}}"),
	}}, nil
}

func TestSkillServerProjectsExecutionContextIntoResolver(t *testing.T) {
	capture := &contextCaptureExecutor{}
	server := NewSkillServer("test-skill", "1.0.0")
	server.RegisterExecutor(capture.Type(), capture)

	response, err := server.Execute(context.Background(), &skillpb.ExecuteRequest{
		NodeId: "node-1", NodeType: capture.Type(),
		Context: &skillpb.ExecutionContext{
			RunId: "run-1", AgentId: "agent-1", Namespace: "operations",
			Variables: map[string]string{"actionCallId": "call-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("execution failed: %#v", response.Error)
	}
	want := map[string]string{
		"runId": "run-1", "agentId": "agent-1", "selfId": "agent-1",
		"namespace": "operations", "actionCallId": "call-1",
	}
	for key, value := range want {
		var got string
		if err := json.Unmarshal(response.Output[key], &got); err != nil {
			t.Fatalf("decode %s: %v", key, err)
		}
		if got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
	if capture.context == nil {
		t.Fatal("resolver did not expose execution context")
	}
	run := capture.context["run"].(map[string]interface{})
	self := capture.context["self"].(map[string]interface{})
	variables := capture.context["vars"].(map[string]interface{})
	if run["id"] != "run-1" || run["agentId"] != "agent-1" ||
		self["id"] != "agent-1" || variables["actionCallId"] != "call-1" {
		t.Fatalf("resolver context = %#v", capture.context)
	}
}

func TestSkillServerAcceptsMissingExecutionContext(t *testing.T) {
	capture := &contextCaptureExecutor{}
	server := NewSkillServer("test-skill", "1.0.0")
	server.RegisterExecutor(capture.Type(), capture)

	response, err := server.Execute(context.Background(), &skillpb.ExecuteRequest{
		NodeId: "node-1", NodeType: capture.Type(),
	})
	if err != nil || response.Error != nil {
		t.Fatalf("execution failed: response=%#v error=%v", response, err)
	}
}
