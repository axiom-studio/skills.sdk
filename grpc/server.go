// Package grpc provides helpers for implementing gRPC-based skills
package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/axiom-studio/skills.sdk/config"
	"github.com/axiom-studio/skills.sdk/executor"
	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"github.com/axiom-studio/skills.sdk/resolver"
	"google.golang.org/grpc"
)

// SkillServer implements the gRPC skill service
type SkillServer struct {
	skillpb.UnimplementedSkillServiceServer

	skillID   string
	version   string
	executors map[string]executor.StepExecutor
	schemas   map[string]*resolver.NodeSchema
	config    config.Runtime
}

// Option configures a SkillServer.
type Option func(*serverOptions)

type serverOptions struct {
	newConfig func(check any) (config.Runtime, error)
	check     any
}

// WithConfig declares the Skill's operator config as the tagged struct T (see
// package config). The SDK derives the shared field schema from T, reports it
// over Health, and decodes the config the platform sends with each Execute
// call for executors to read via config.From.
func WithConfig[T any](opts ...config.Option) Option {
	return func(o *serverOptions) {
		o.newConfig = func(check any) (config.Runtime, error) {
			var fn func(context.Context, T) config.CheckResult
			if check != nil {
				var ok bool
				if fn, ok = check.(func(context.Context, T) config.CheckResult); !ok {
					return nil, fmt.Errorf("config: WithConfigCheck takes %T, want func(context.Context, %T) config.CheckResult", check, *new(T))
				}
			}
			return config.NewSpec[T](fn, opts...)
		}
	}
}

// WithConfigCheck declares a check of the Skill's config (for example, that an
// API key works). The platform runs it through the Health RPC against the
// config values it sends. T must be the type given to WithConfig.
func WithConfigCheck[T any](check func(ctx context.Context, cfg T) config.CheckResult) Option {
	return func(o *serverOptions) { o.check = check }
}

// NewSkillServer creates a new skill server. It panics on an invalid config
// declaration, which is a programming error caught at startup.
func NewSkillServer(skillID, version string, opts ...Option) *SkillServer {
	var o serverOptions
	for _, opt := range opts {
		opt(&o)
	}
	s := &SkillServer{
		skillID:   skillID,
		version:   version,
		executors: make(map[string]executor.StepExecutor),
		schemas:   make(map[string]*resolver.NodeSchema),
	}
	switch {
	case o.newConfig != nil:
		rt, err := o.newConfig(o.check)
		if err != nil {
			panic(err)
		}
		s.config = rt
	case o.check != nil:
		panic("config: WithConfigCheck requires WithConfig")
	}
	return s
}

// RegisterExecutor registers an executor for a node type.
// Optionally pass a config struct to auto-generate the schema.
func (s *SkillServer) RegisterExecutor(nodeType string, exec executor.StepExecutor, configType ...interface{}) {
	s.executors[nodeType] = exec

	// Auto-generate schema from config type if provided
	if len(configType) > 0 && configType[0] != nil {
		s.schemas[nodeType] = resolver.GenerateSchema(nodeType, configType[0])
	}
}

// RegisterExecutorWithSchema registers an executor with a manually defined schema
func (s *SkillServer) RegisterExecutorWithSchema(nodeType string, exec executor.StepExecutor, schema *resolver.NodeSchema) {
	s.executors[nodeType] = exec
	s.schemas[nodeType] = schema
}

// Execute implements skillpb.SkillServer
func (s *SkillServer) Execute(ctx context.Context, req *skillpb.ExecuteRequest) (*skillpb.ExecuteResponse, error) {
	exec, ok := s.executors[req.NodeType]
	if !ok {
		return &skillpb.ExecuteResponse{
			Error: &skillpb.Error{
				Message: fmt.Sprintf("unknown node type: %s", req.NodeType),
				Type:    "validation",
			},
		}, nil
	}

	// Deserialize config and input
	stepConfig := make(map[string]interface{})
	for k, v := range req.Config {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			stepConfig[k] = val
		}
	}

	input := make(map[string]interface{})
	for k, v := range req.Input {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			input[k] = val
		}
	}

	// Deserialize bindings
	bindings := make(map[string]interface{})
	for k, v := range req.Bindings {
		var val interface{}
		if err := json.Unmarshal(v, &val); err == nil {
			bindings[k] = val
		}
	}

	// Create step definition
	step := &executor.StepDefinition{
		Id:     req.NodeId,
		Type:   req.NodeType,
		Config: stepConfig,
	}

	// Create resolver using the shared implementation
	runContext, selfContext, variables := resolverContext(req.GetContext())
	res := resolver.New(resolver.Config{
		Bindings:  bindings,
		Nodes:     map[string]interface{}{"prev": input},
		Prev:      input,
		Variables: variables,
		Run:       runContext,
		Self:      selfContext,
	})

	// Operator config comes only from the execution context, which the
	// platform resolves for the call's authenticated tenant; never from
	// action arguments or variables.
	if s.config != nil {
		ec := req.GetContext()
		bound, err := s.config.Bind(ctx, config.Call{
			TenantID: ec.GetTenantId(),
			Version:  ec.GetConfigVersion(),
			Values:   ec.GetConfig(),
		})
		if err != nil {
			return &skillpb.ExecuteResponse{
				Error: &skillpb.Error{Message: err.Error(), Type: "config"},
			}, nil
		}
		ctx = bound
	}

	// Execute
	result, err := exec.Execute(ctx, step, res)
	if err != nil {
		return &skillpb.ExecuteResponse{
			Error: &skillpb.Error{
				Message: err.Error(),
				Type:    "execution",
			},
		}, nil
	}

	// Serialize output
	output := make(map[string][]byte)
	for k, v := range result.Output {
		data, _ := json.Marshal(v)
		output[k] = data
	}

	return &skillpb.ExecuteResponse{
		Output:   output,
		NextStep: result.NextStep,
	}, nil
}

func resolverContext(execution *skillpb.ExecutionContext) (
	map[string]interface{},
	map[string]interface{},
	map[string]interface{},
) {
	run := make(map[string]interface{})
	self := make(map[string]interface{})
	variables := make(map[string]interface{})
	if execution == nil {
		return run, self, variables
	}
	if execution.RunId != "" {
		run["id"] = execution.RunId
	}
	if execution.AgentId != "" {
		run["agentId"] = execution.AgentId
		self["id"] = execution.AgentId
	}
	if execution.Namespace != "" {
		run["namespace"] = execution.Namespace
		self["namespace"] = execution.Namespace
	}
	for key, value := range execution.Variables {
		variables[key] = value
	}
	return run, self, variables
}

// GetNodeTypes implements skillpb.SkillServer
func (s *SkillServer) GetNodeTypes(ctx context.Context, req *skillpb.GetNodeTypesRequest) (*skillpb.GetNodeTypesResponse, error) {
	types := make([]string, 0, len(s.executors))
	for t := range s.executors {
		types = append(types, t)
	}
	return &skillpb.GetNodeTypesResponse{NodeTypes: types}, nil
}

// GetNodeSchema implements skillpb.SkillServer
func (s *SkillServer) GetNodeSchema(ctx context.Context, req *skillpb.GetNodeSchemaRequest) (*skillpb.GetNodeSchemaResponse, error) {
	schema, ok := s.schemas[req.NodeType]
	if !ok {
		return nil, fmt.Errorf("unknown node type: %s", req.NodeType)
	}

	jsonSchema, err := schema.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema: %w", err)
	}

	return &skillpb.GetNodeSchemaResponse{Schema: jsonSchema}, nil
}

// Health implements skillpb.SkillServer
func (s *SkillServer) Health(ctx context.Context, req *skillpb.HealthRequest) (*skillpb.HealthResponse, error) {
	resp := &skillpb.HealthResponse{
		Healthy: true,
		SkillId: s.skillID,
		Version: s.version,
	}
	if s.config == nil {
		return resp, nil
	}
	resp.Config = &skillpb.ConfigStatus{
		Schema:         s.config.Schema().JSON(),
		HasConfigCheck: s.config.HasCheck(),
	}
	if req.GetConfigCheck() {
		if result, ok := s.config.Check(ctx, req.GetConfigValues()); ok {
			check := &skillpb.ConfigCheckResult{Status: string(result.Status)}
			for _, c := range result.Checks {
				check.Checks = append(check.Checks, &skillpb.ConfigCheck{
					Id: c.ID, Label: c.Label, Status: string(c.Status), Detail: c.Detail,
				})
			}
			resp.Config.ConfigCheck = check
		}
	}
	return resp, nil
}

// Serve starts the Skill's gRPC endpoint. Discovery and lifecycle ownership
// belong to the installing runtime; a Skill never phones home to a product.
func (s *SkillServer) Serve(port string) error {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	skillpb.RegisterSkillServiceServer(grpcServer, s)

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		grpcServer.GracefulStop()
	}()

	return grpcServer.Serve(lis)
}
