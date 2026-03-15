# Axiom Skills SDK

SDK for building Axiom skill plugins.

## Overview

Skills extend Axiom with custom node types. Each skill is a Go plugin that implements the `SkillPlugin` interface.

## Quick Start

### 1. Create skill.yaml

```yaml
apiVersion: axiom.studio/v1
kind: Skill

metadata:
  id: my-skill
  name: "My Custom Skill"
  description: "Custom node types for Axiom"
  author: "Your Name"
  authorEmail: "you@example.com"
  version: 1.0.0
  license: Proprietary
  category: custom
  tags:
    - custom

spec:
  executorType: plugin
  nodeTypes:
    - my_node_type
  plugin:
    binary:
      linux-amd64: executors/my-skill-linux-amd64.so
  dependencies:
    standard:
      - logger
      - http-client
```

### 2. Implement the Plugin

```go
//go:build plugin

package main

import (
    "context"
    
    "github.com/axiom-studio/skills.sdk/executor"
    "github.com/axiom-studio/skills.sdk/skill"
    "github.com/axiom-studio/skills.sdk/deps"
)

type MySkillPlugin struct {
    logger deps.Logger
}

func (p *MySkillPlugin) Initialize(config map[string]interface{}) error {
    if logger, ok := config[deps.LoggerKey].(deps.Logger); ok {
        p.logger = logger
    }
    return nil
}

func (p *MySkillPlugin) GetExecutors() []executor.StepExecutor {
    return []executor.StepExecutor{
        &MyNodeExecutor{logger: p.logger},
    }
}

func (p *MySkillPlugin) Shutdown() error {
    return nil
}

// MyNodeExecutor implements executor.StepExecutor
type MyNodeExecutor struct {
    logger deps.Logger
}

func (e *MyNodeExecutor) Type() string {
    return "my_node_type"
}

func (e *MyNodeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
    // Your logic here
    return &executor.StepResult{
        Output: map[string]interface{}{
            "result": "success",
        },
    }, nil
}

// Plugin symbol that Axiom will load
var Plugin = &MySkillPlugin{}
```

### 3. Build the Plugin

```bash
go build -buildmode=plugin -o executors/my-skill-linux-amd64.so
```

## Interfaces

### executor.StepExecutor

The core interface for node execution:

```go
type StepExecutor interface {
    Execute(ctx context.Context, step *StepDefinition, resolver TemplateResolver) (*StepResult, error)
    Type() string
}
```

### skill.SkillPlugin

The plugin interface:

```go
type SkillPlugin interface {
    GetExecutors() []executor.StepExecutor
    Initialize(config map[string]interface{}) error
    Shutdown() error
}
```

## Dependencies

Skills can request dependencies in `skill.yaml`:

- `logger` - Structured logging
- `http-client` - HTTP client for external APIs
- `k8s-client` - Kubernetes client for cluster operations
- `secrets` - Secrets provider for sensitive data

Dependencies are injected via `Initialize()`:

```go
func (p *MyPlugin) Initialize(config map[string]interface{}) error {
    p.logger = config[deps.LoggerKey].(deps.Logger)
    p.httpClient = config[deps.HTTPClientKey].(deps.HTTPClient)
    return nil
}
```

## License

Proprietary - All rights reserved.