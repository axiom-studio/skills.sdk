# Axiom Skills SDK

SDK for building Axiom skills as gRPC services.

## Overview

Skills extend Axiom with custom node types. Each skill is a standalone gRPC server that implements the `SkillService` interface. This architecture allows skills to be:

- Written in any language (Go, Python, Rust, etc.)
- Deployed independently
- Scaled horizontally
- Isolated from the main Axiom process

## Architecture

```
┌─────────────────┐     gRPC      ┌─────────────────┐
│                 │◄──────────────►│  skill-database │
│     Cortex      │               │  (port 50051)   │
│   (Axiom Core)  │               └─────────────────┘
│                 │     gRPC      ┌─────────────────┐
│                 │◄──────────────►│   skill-k8s     │
│                 │               │  (port 50052)   │
└─────────────────┘               └─────────────────┘
```

## Quick Start

### 1. Create skill.yaml

```yaml
apiVersion: skills.axiom.dev/v1
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
  executorType: grpc
  nodeTypes:
    - my_query
    - my_insert
  grpc:
    address: "localhost:50051"
    binary:
      linux-amd64: bin/my-skill-linux-amd64
      darwin-arm64: bin/my-skill-darwin-arm64
```

### 2. Implement the gRPC Server (Go)

```go
package main

import (
    "context"
    "log"
    "net"

    "github.com/axiom-studio/skills.sdk/grpc"
    "github.com/axiom-studio/skills.sdk/grpc/skillpb"
    "google.golang.org/grpc"
)

type MySkillServer struct {
    grpc.SkillServer
}

func (s *MySkillServer) Execute(ctx context.Context, req *skillpb.ExecuteRequest) (*skillpb.ExecuteResponse, error) {
    // Your node execution logic here
    return &skillpb.ExecuteResponse{
        Output: map[string]string{
            "result": "success",
        },
    }, nil
}

func main() {
    lis, _ := net.Listen("tcp", ":50051")
    s := grpc.NewServer()
    
    skill := &MySkillServer{}
    skill.Init("my-skill", "1.0.0")
    skill.RegisterExecutor("my_query", skill.Execute)
    skill.RegisterExecutor("my_insert", skill.Execute)
    
    skillpb.RegisterSkillServiceServer(s, skill)
    s.Serve(lis)
}
```

### 3. Build and Run

```bash
# Build
go build -o bin/my-skill .

# Run
./bin/my-skill
```

## gRPC Service Definition

```protobuf
service SkillService {
    rpc GetNodeTypes(GetNodeTypesRequest) returns (GetNodeTypesResponse);
    rpc Execute(ExecuteRequest) returns (ExecuteResponse);
    rpc Health(HealthRequest) returns (HealthResponse);
}
```

### ExecuteRequest

```protobuf
message ExecuteRequest {
    string node_type = 1;           // Node type to execute
    string node_id = 2;             // Unique node instance ID
    string node_name = 3;           // Human-readable node name
    map<string, string> config = 4; // Node configuration
    map<string, string> input = 5;  // Input from trigger/previous nodes
    map<string, string> bindings = 6; // Resolved bindings
}
```

### ExecuteResponse

```protobuf
message ExecuteResponse {
    map<string, string> output = 1; // Output data
    string error = 2;               // Error message if failed
}
```

## Using the SDK

### Install

```bash
go get github.com/axiom-studio/skills.sdk@latest
```

### Schema-Based Configuration

Define your node config as a typed struct. The SDK automatically:

1. **Resolves expressions** - `{{bindings.xxx}}` in string fields are auto-resolved
2. **Generates schemas** - Atlas can query `GetNodeSchema` to show the config UI

```go
import "github.com/axiom-studio/skills.sdk/resolver"

// Define your config schema
type DBQueryConfig struct {
    ConnectionString string `json:"connectionString" description:"Database connection string, supports {{bindings.xxx}}"`
    Driver           string `json:"driver" default:"postgres" options:"PostgreSQL:postgres,MySQL:mysql" description:"Database driver"`
    Query            string `json:"query" description:"SQL query to execute"`
    Args             []interface{} `json:"args" description:"Query parameters"`
}

func (e *DBQueryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, templateResolver executor.TemplateResolver) (*executor.StepResult, error) {
    var cfg DBQueryConfig
    if err := resolver.ResolveConfig(step.Config, &cfg, templateResolver.(*resolver.Resolver)); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }

    // All fields are typed and resolved!
    db, _ := sql.Open(cfg.Driver, cfg.ConnectionString)
    rows, _ := db.QueryContext(ctx, cfg.Query, cfg.Args...)
}

// Register with auto-generated schema
server.RegisterExecutor("db-query", &DBQueryExecutor{}, DBQueryConfig{})
```

### Full UI Schema with SchemaBuilder

For full control over the UI, use `SchemaBuilder`:

```go
var DBQuerySchema = resolver.NewSchemaBuilder("db-query").
    WithName("Database Query").
    WithCategory("database").
    WithIcon("database").
    WithDescription("Execute SQL queries against a database").
    AddSection("Connection").
        AddExpressionField("connectionString", "Connection String",
            resolver.WithRequired(),
            resolver.WithPlaceholder("postgresql://user:pass@host:5432/db"),
            resolver.WithHint("Supports {{bindings.xxx}} for secure credential access"),
        ).
        AddSelectField("driver", "Driver", []resolver.SelectOption{
            {Label: "PostgreSQL", Value: "postgres", Icon: "database"},
            {Label: "MySQL", Value: "mysql", Icon: "database"},
        }, resolver.WithDefault("postgres")).
    EndSection().
    AddSection("Query").Collapsible(true).
        AddCodeField("query", "SQL Query", "sql",
            resolver.WithRequired(),
            resolver.WithHeight(150),
        ).
        AddTagsField("args", "Parameters").
    EndSection().
    Build()

// Register with schema
server.RegisterExecutorWithSchema("db-query", &DBQueryExecutor{}, DBQuerySchema)
```

### Field Types

| Type | UI Widget | Use Case |
|------|-----------|----------|
| `text` | Text input | Short text values |
| `textarea` | Multi-line input | Long text, descriptions |
| `number` | Number input | Numeric values |
| `select` | Dropdown | Choose from options |
| `multiselect` | Multi-select dropdown | Multiple choices |
| `toggle` | Switch | Boolean values |
| `slider` | Range slider | Numeric range |
| `keyvalue` | Key-value editor | Headers, params |
| `tags` | Chip input | Tags, arrays |
| `code` | Code editor | SQL, scripts |
| `json` | JSON editor | Complex objects |
| `cron` | Cron builder | Schedule expressions |
| `expression` | Template input | {{}} expressions |

### Field Options

```go
// Required field
resolver.WithRequired()

// Default value
resolver.WithDefault("postgres")

// Placeholder text
resolver.WithPlaceholder("Enter value...")

// Help text
resolver.WithHint("This field supports {{bindings.xxx}}")

// Sensitive (password field)
resolver.WithSensitive()

// Validation
resolver.WithValidation("^[a-z]+$", "Only lowercase letters")

// Min/Max for numbers
resolver.WithMinMax(0, 100)

// Conditional visibility
resolver.WithShowIf("authType", "basic")
resolver.WithShowIfOneOf("method", "POST", "PUT")

// Code/JSON editor height
resolver.WithHeight(200)

// Textarea rows
resolver.WithRows(4)

// Number suffix (e.g., "ms", "px")
resolver.WithSuffix("ms")

// Slider step
resolver.WithStep(0.1)
```

### Sections

```go
AddSection("Advanced Options").
    Collapsible(true).  // Can be collapsed
    // Fields...
EndSection()
```

### Generated Schema JSON

```json
{
  "nodeType": "db-query",
  "name": "Database Query",
  "category": "database",
  "icon": "database",
  "sections": [
    {
      "title": "Connection",
      "fields": [
        {
          "key": "connectionString",
          "label": "Connection String",
          "type": "expression",
          "required": true,
          "placeholder": "postgresql://user:pass@host:5432/db",
          "hint": "Supports {{bindings.xxx}} for secure credential access"
        },
        {
          "key": "driver",
          "label": "Driver",
          "type": "select",
          "default": "postgres",
          "options": [
            {"label": "PostgreSQL", "value": "postgres", "icon": "database"},
            {"label": "MySQL", "value": "mysql", "icon": "database"}
          ]
        }
      ]
    }
  ]
}
```

### Struct Tags for Auto-Generated Schemas

| Tag | Example | Description |
|-----|---------|-------------|
| `json` | `json:"fieldName"` | Maps config key to struct field |
| `default` | `default:"postgres"` | Default value if field is missing |
| `description` | `description:"SQL query"` | Shown in UI as hint |
| `placeholder` | `placeholder:"SELECT..."` | Placeholder text in input |
| `options` | `options:"PostgreSQL:postgres,MySQL:mysql"` | Dropdown options |
| `sensitive` | `sensitive:"true"` | Marks as password field |
| `showIf` | `showIf:"authType=basic"` | Conditional visibility |

### SkillServer Helper

```go
import "github.com/axiom-studio/skills.sdk/grpc"

type MyServer struct {
    grpc.SkillServer  // Embed for default implementations
}

func (s *MyServer) ExecuteQuery(ctx context.Context, req *skillpb.ExecuteRequest) (*skillpb.ExecuteResponse, error) {
    // Handle my_query node type
    return &skillpb.ExecuteResponse{
        Output: map[string]string{"rows": "[]"},
    }, nil
}

func main() {
    s := &MyServer{}
    s.Init("my-skill", "1.0.0")
    
    // Register node type handlers
    s.RegisterExecutor("my_query", s.ExecuteQuery)
    
    // Start server
    s.Serve(":50051")
}
```

## Node Types

Each skill can register multiple node types. Node types should be namespaced to avoid collisions:

- `db-query` - Database query
- `db-insert` - Database insert
- `k8s-get` - Kubernetes get resource
- `k8s-list` - Kubernetes list resources

## Health Checks

Skills must implement the `Health` RPC for monitoring:

```go
func (s *MyServer) Health(ctx context.Context, req *skillpb.HealthRequest) (*skillpb.HealthResponse, error) {
    return &skillpb.HealthResponse{
        Healthy: true,
        SkillId: "my-skill",
        Version: "1.0.0",
    }, nil
}
```

## Deployment

### With Hermes (Skill Manager)

Hermes manages skill lifecycle in Kubernetes:

```yaml
# values.yaml for atlas chart
hermes:
  enabled: true
  skills:
    - name: skill-database
      repository: https://github.com/axiom-studio/skills.skill-database
      version: main
      port: 50051
```

### Standalone

Run skills as separate processes:

```bash
# Start skill on port 50051
SKILL_PORT=50051 ./bin/my-skill

# Configure cortex to connect
# In skill.yaml: grpc.address: "localhost:50051"
```

## Examples

See the example skills:

- [skill-database](https://github.com/axiom-studio/skills.skill-database) - Database operations (query, insert, update, delete)
- [skill-k8s](https://github.com/axiom-studio/skills.skill-k8s) - Kubernetes operations (get, list, patch, scale)

## Migration from Go Plugins

If you have existing Go plugin skills, migrate to gRPC:

1. Change `executorType: plugin` to `executorType: grpc`
2. Replace plugin binary with gRPC server
3. Update `skill.yaml` with gRPC config
4. Implement the gRPC `SkillService` interface

## License

Proprietary - All rights reserved.