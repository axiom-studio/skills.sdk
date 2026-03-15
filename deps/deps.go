// Package deps defines dependency interfaces that Axiom injects into skills.
// Skills request these via the SkillDependencies field in skill.yaml.
package deps

import (
	"context"
	"net/http"
)

// Dependency keys passed to SkillPlugin.Initialize
const (
	// LoggerKey is the key for the logger dependency
	LoggerKey = "logger"
	// HTTPClientKey is the key for the HTTP client dependency
	HTTPClientKey = "http-client"
	// K8sClientKey is the key for the Kubernetes client dependency
	K8sClientKey = "k8s-client"
	// SecretsKey is the key for the secrets provider dependency
	SecretsKey = "secrets"
)

// Logger is the logging interface provided to skills
type Logger interface {
	Debug(args ...interface{})
	Debugf(template string, args ...interface{})
	Debugw(msg string, keysAndValues ...interface{})

	Info(args ...interface{})
	Infof(template string, args ...interface{})
	Infow(msg string, keysAndValues ...interface{})

	Warn(args ...interface{})
	Warnf(template string, args ...interface{})
	Warnw(msg string, keysAndValues ...interface{})

	Error(args ...interface{})
	Errorf(template string, args ...interface{})
	Errorw(msg string, keysAndValues ...interface{})

	Fatal(args ...interface{})
	Fatalf(template string, args ...interface{})
	Fatalw(msg string, keysAndValues ...interface{})
}

// HTTPClient is the HTTP client interface provided to skills
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
	Get(url string) (*http.Response, error)
	Post(url, contentType string, body interface{}) (*http.Response, error)
}

// K8sClient is the Kubernetes client interface provided to skills
// Skills can use this to interact with Kubernetes resources
type K8sClient interface {
	// CoreV1 provides access to core v1 resources (pods, services, etc.)
	// Returns the typed client interface for type safety
	GetTypedClient() interface{}

	// GetNamespace returns the namespace the skill is running in
	GetNamespace() string

	// ListPods lists pods in a namespace
	ListPods(ctx context.Context, namespace string, opts interface{}) (interface{}, error)

	// GetPod gets a specific pod
	GetPod(ctx context.Context, namespace, name string) (interface{}, error)

	// CreateJob creates a Kubernetes job
	CreateJob(ctx context.Context, namespace string, job interface{}) (interface{}, error)
}

// SecretsProvider is the secrets provider interface for skills
type SecretsProvider interface {
	// GetSecret retrieves a secret value by key
	GetSecret(ctx context.Context, key string) (string, error)

	// GetSecrets retrieves multiple secret values
	GetSecrets(ctx context.Context, keys ...string) (map[string]string, error)

	// SetSecret stores a secret value
	SetSecret(ctx context.Context, key, value string) error
}