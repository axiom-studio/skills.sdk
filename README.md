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

Skills are distributed as **pre-built binaries**. Hermes (the skill manager) does not build skills at runtime. Instead, it downloads or loads binaries that you provide. This means:

- **Faster startup**: No compilation delay when installing or updating skills
- **Reproducible builds**: Binaries are built once in CI, not on every user's machine
- **No build dependencies**: Users don't need Go, Rust, or any toolchain installed
- **Smaller attack surface**: No compiler or build tools required on the target system

The binary distribution flow:

```
Developer CI ──build──► binaries/ ──commit/publish──► Skill Repo / Release
                                                          │
Hermes ◄──download/load── skill.yaml (binary paths or URLs)
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
      linux-amd64: binaries/my-skill-linux-amd64
      darwin-arm64: binaries/my-skill-darwin-arm64
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

### 3. Build Binaries

Skills must ship pre-built binaries. Build for each target platform:

```bash
# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o binaries/my-skill-linux-amd64 .

# Linux arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o binaries/my-skill-linux-arm64 .

# macOS arm64 (Apple Silicon)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o binaries/my-skill-darwin-arm64 .
```

See [Building Binaries](#building-binaries) for full details and CI automation.

### 4. Run

```bash
./binaries/my-skill-linux-amd64
```

## Binary Distribution

Skills **must** provide pre-built binaries. Hermes no longer builds skills at runtime. There are two ways to distribute binaries:

### Method 1: Git-Tracked Binaries

Commit binaries directly to your skill repository. Hermes loads them from the local filesystem.

```yaml
spec:
  grpc:
    binary:
      linux-amd64: binaries/my-skill-linux-amd64
      darwin-arm64: binaries/my-skill-darwin-arm64
```

**Pros**: Simple, no external hosting needed, works offline.
**Cons**: Increases repo size, requires `git lfs` for large binaries.

**Setup with Git LFS** (recommended for binaries over 50MB):

```bash
git lfs install
git lfs track "binaries/*"
echo "binaries/*" >> .gitattributes
git add .gitattributes
```

### Method 2: Remote Binaries

Host binaries externally and provide download URLs. Hermes downloads them on first use.

```yaml
spec:
  grpc:
    binaryUrls:
      linux-amd64: https://releases.example.com/my-skill/v1.0/my-skill-linux-amd64
      linux-arm64: https://releases.example.com/my-skill/v1.0/my-skill-linux-arm64
      darwin-arm64: https://releases.example.com/my-skill/v1.0/my-skill-darwin-arm64
```

**Pros**: Keeps repo small, easy versioning via URL paths.
**Cons**: Requires internet access, depends on external hosting.

**Common hosting options**:
- GitHub Releases (attach binaries to releases)
- AWS S3 / CloudFront
- Cloudflare R2
- Any static file server

### Binary Naming Convention

Binaries should follow the pattern: `<skill-id>-<os>-<arch>`

| Platform | OS | Arch | Binary Name Example |
|----------|----|------|---------------------|
| Linux x86_64 | linux | amd64 | `my-skill-linux-amd64` |
| Linux ARM64 | linux | arm64 | `my-skill-linux-arm64` |
| macOS Apple Silicon | darwin | arm64 | `my-skill-darwin-arm64` |

### Supported Platforms

| Platform ID | OS | Architecture |
|-------------|----|--------------|
| `linux-amd64` | Linux | x86_64 |
| `linux-arm64` | Linux | ARM64 |
| `darwin-arm64` | macOS | ARM64 (Apple Silicon) |

## Building Binaries

### Manual Build Commands

Build static binaries for all supported platforms:

```bash
# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o binaries/my-skill-linux-amd64 .

# Linux arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o binaries/my-skill-linux-arm64 .

# macOS arm64
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o binaries/my-skill-darwin-arm64 .
```

Key flags:
- `CGO_ENABLED=0`: Produces fully static binaries with no C dependencies
- `GOOS`/`GOARCH`: Cross-compilation targets
- `-o binaries/...`: Output to the `binaries/` directory

### Build All Platforms (One Command)

```bash
#!/bin/bash
set -e

SKILL_ID="my-skill"
mkdir -p binaries

platforms=(
  "linux/amd64"
  "linux/arm64"
  "darwin/arm64"
)

for platform in "${platforms[@]}"; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  echo "Building ${SKILL_ID}-${GOOS}-${GOARCH}..."
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -o "binaries/${SKILL_ID}-${GOOS}-${GOARCH}" .
done

echo "All binaries built in binaries/"
```

### CI Workflow

Use GitHub Actions to automate binary builds on every release. A reference workflow is available at `.github/workflows/build-binaries.yml`:

```yaml
name: Build Skill Binaries

on:
  release:
    types: [published]
  workflow_dispatch:

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - os: linux
            arch: amd64
          - os: linux
            arch: arm64
          - os: darwin
            arch: arm64

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Build binary
        env:
          CGO_ENABLED: 0
          GOOS: ${{ matrix.os }}
          GOARCH: ${{ matrix.arch }}
        run: |
          go build -o binaries/my-skill-${{ matrix.os }}-${{ matrix.arch }} .

      - name: Upload to release
        if: github.event_name == 'release'
        uses: softprops/action-gh-release@v2
        with:
          files: binaries/my-skill-${{ matrix.os }}-${{ matrix.arch }}
```

This workflow builds binaries for all platforms and attaches them to GitHub Releases, which you can then reference via `binaryUrls` in your `skill.yaml`.

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

Hermes manages the skill lifecycle in Kubernetes. It loads pre-built binaries from your repository or downloads them from remote URLs.

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

Hermes reads the `skill.yaml` from the repository, locates the binaries via the `binary` or `binaryUrls` fields, and launches the skill process. No build step occurs at runtime.

### Standalone

Run skills as separate processes:

```bash
# Start skill on port 50051
SKILL_PORT=50051 ./binaries/my-skill-linux-amd64

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
5. **Build and ship pre-built binaries** (Hermes no longer builds at runtime)

## License

Proprietary - All rights reserved.
