// Package skill defines the interfaces for Axiom skill plugins.
// Skills are distributed as Go plugins that implement SkillPlugin.
package skill

import "github.com/axiom-studio/skills.sdk/executor"

// SkillPlugin is the interface that all skill plugins must implement.
// Plugins must export a symbol named "Plugin" that implements this interface.
//
// Example:
//
//	var Plugin = &MySkillPlugin{}
type SkillPlugin interface {
	// GetExecutors returns all step executors provided by this skill
	GetExecutors() []executor.StepExecutor

	// Initialize is called once when the plugin is loaded
	// config contains dependencies injected by Axiom via DependencyRegistry
	Initialize(config map[string]interface{}) error

	// Shutdown is called when the plugin is being unloaded (during restart)
	Shutdown() error
}

// SkillManifest represents the parsed skill.yaml file
type SkillManifest struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Metadata   SkillMetadata `yaml:"metadata"`
	Spec       SkillSpec     `yaml:"spec"`
}

// SkillMetadata contains metadata about the skill
type SkillMetadata struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Author      string   `yaml:"author"`
	AuthorEmail string   `yaml:"authorEmail"`
	Version     string   `yaml:"version"`
	License     string   `yaml:"license"`
	Category    string   `yaml:"category"`
	Tags        []string `yaml:"tags"`
	Icon        string   `yaml:"icon"`
	Color       string   `yaml:"color"`
}

// SkillSpec defines the skill's capabilities
type SkillSpec struct {
	ExecutorType string             `yaml:"executorType"` // "plugin", "grpc", or "mcp"
	NodeTypes    []string           `yaml:"nodeTypes"`
	Tools        []ToolDefinition   `yaml:"tools,omitempty"`        // AI-callable tools
	MCP          MCPConfig          `yaml:"mcp,omitempty"`          // MCP server configuration
	Plugin       PluginConfig       `yaml:"plugin,omitempty"`
	GRPC         GRPCConfig         `yaml:"grpc,omitempty"`
	Dependencies SkillDependencies  `yaml:"dependencies"`
	Permissions  []string           `yaml:"permissions"`
	Requirements []SkillRequirement `yaml:"requirements"`
}

// ToolDefinition defines an AI-callable tool provided by a skill
type ToolDefinition struct {
	Name        string                 `yaml:"name" json:"name"`
	Description string                 `yaml:"description" json:"description"`
	Parameters  map[string]interface{} `yaml:"parameters" json:"parameters"` // JSON Schema
	Config      map[string]interface{} `yaml:"config" json:"config"`         // Tool-specific config (e.g., MCP server config)
}

// MCPConfig specifies the MCP server configuration
type MCPConfig struct {
	Command string            `yaml:"command"`           // Command to run (e.g., "npx", "uvx")
	Args    []string          `yaml:"args"`              // Command arguments
	Env     map[string]string `yaml:"env,omitempty"`     // Environment variables
	Timeout int               `yaml:"timeout,omitempty"` // Connection timeout in seconds (default: 300)
}

// PluginConfig specifies the plugin binary locations
type PluginConfig struct {
	Binary map[string]string `yaml:"binary"` // platform -> path (e.g., "linux-amd64": "executors/core.so")
}

// GRPCConfig specifies the gRPC skill configuration
type GRPCConfig struct {
	Address string            `yaml:"address,omitempty"` // gRPC address (e.g., "localhost:50051")
	Binary  map[string]string `yaml:"binary,omitempty"`  // platform -> path to skill binary
	Port    int               `yaml:"port,omitempty"`    // Port to run the skill on (if starting process)
}

// SkillDependencies defines what the skill needs from Axiom
type SkillDependencies struct {
	Standard []string           `yaml:"standard"` // "logger", "http-client", "k8s-client", "secrets"
	Custom   []CustomDependency `yaml:"custom"`
}

// CustomDependency defines a custom dependency
type CustomDependency struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Optional    bool   `yaml:"optional"`
	Description string `yaml:"description"`
}

// SkillRequirement defines a runtime requirement
type SkillRequirement struct {
	Type        string `yaml:"type"`
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description"`
	Optional    bool   `yaml:"optional"`
}

// LoadedPlugin represents a loaded plugin in memory
type LoadedPlugin struct {
	SkillID   string
	Manifest  *SkillManifest
	Plugin    SkillPlugin
	Executors []executor.StepExecutor
	Tools     []ToolDefinition // AI-callable tools from this skill
}