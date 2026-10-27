package config

import (
	"container/list"
	"context"
	"fmt"
	"reflect"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"
)

// CheckStatus is the outcome of a config check or one of its rows.
type CheckStatus string

const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// Check is one row of a config check.
type Check struct {
	ID     string      `json:"id"`
	Label  string      `json:"label"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// CheckResult is what a Skill's config check returns. An empty Status is
// derived from the worst row (ok when there are none).
type CheckResult struct {
	Status CheckStatus `json:"status"`
	Checks []Check     `json:"checks"`
}

// Call is the config part of one call, as the platform sent it.
type Call struct {
	// TenantID is the call's authenticated tenant (ExecutionContext.tenant_id).
	TenantID string
	// Version is the version of the tenant's effective config
	// (ExecutionContext.config_version). Empty disables caching.
	Version string
	// Values is the tenant's effective config (ExecutionContext.config). Nil
	// means declared defaults.
	Values *structpb.Struct
}

// Runtime is the non-generic face of a Spec, used by the gRPC server.
type Runtime interface {
	// Bind decodes the call's config and returns ctx carrying it for From. It
	// fails when a value has the wrong type or breaks a declared constraint.
	Bind(ctx context.Context, call Call) (context.Context, error)
	Schema() *Schema
	// HasCheck reports whether a config check is declared.
	HasCheck() bool
	// Check decodes values and runs the declared check against them; ok is
	// false when no check is declared. A decode error is a failed check.
	Check(ctx context.Context, values *structpb.Struct) (result CheckResult, ok bool)
}

// CacheSize bounds the decoded configs a Spec keeps, keyed by
// (tenant, config version).
const CacheSize = 256

// Spec is a Skill's declared config type T plus its optional check.
type Spec[T any] struct {
	decl     *declaration
	check    func(context.Context, T) CheckResult
	defaults T

	mu    sync.Mutex
	cache map[cacheKey]*list.Element
	lru   *list.List // of *cacheEntry[T], most recent first
	size  int
}

type cacheKey struct{ tenant, version string }

type cacheEntry[T any] struct {
	key cacheKey
	cfg T
}

// NewSpec validates T's declaration. check may be nil.
func NewSpec[T any](check func(context.Context, T) CheckResult, opts ...Option) (*Spec[T], error) {
	d, err := declare(reflect.TypeFor[T](), opts)
	if err != nil {
		return nil, err
	}
	s := &Spec[T]{decl: d, check: check, cache: map[cacheKey]*list.Element{}, lru: list.New(), size: CacheSize}
	if err := d.decodeInto(reflect.ValueOf(&s.defaults).Elem(), nil); err != nil {
		return nil, err
	}
	return s, nil
}

// Decode returns T for values: declared defaults overlaid with the values.
func (s *Spec[T]) Decode(values *structpb.Struct) (T, error) {
	if values == nil {
		return s.defaults, nil
	}
	var out T
	if err := s.decl.decodeInto(reflect.ValueOf(&out).Elem(), values); err != nil {
		return out, fmt.Errorf("invalid config: %w", err)
	}
	return out, nil
}

// decodeCall decodes a call's config, reusing the cached value for the
// call's (tenant, version) when there is one.
func (s *Spec[T]) decodeCall(call Call) (T, error) {
	if call.Version == "" {
		return s.Decode(call.Values)
	}
	key := cacheKey{call.TenantID, call.Version}
	s.mu.Lock()
	if el, ok := s.cache[key]; ok {
		s.lru.MoveToFront(el)
		cfg := el.Value.(*cacheEntry[T]).cfg
		s.mu.Unlock()
		return cfg, nil
	}
	s.mu.Unlock()

	cfg, err := s.Decode(call.Values)
	if err != nil {
		return cfg, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.cache[key]; ok { // decoded concurrently
		s.lru.MoveToFront(el)
		return el.Value.(*cacheEntry[T]).cfg, nil
	}
	// A tenant's older versions are never asked for again.
	for el := s.lru.Front(); el != nil; {
		next := el.Next()
		if e := el.Value.(*cacheEntry[T]); e.key.tenant == key.tenant {
			s.lru.Remove(el)
			delete(s.cache, e.key)
		}
		el = next
	}
	s.cache[key] = s.lru.PushFront(&cacheEntry[T]{key: key, cfg: cfg})
	for s.lru.Len() > s.size {
		oldest := s.lru.Back()
		s.lru.Remove(oldest)
		delete(s.cache, oldest.Value.(*cacheEntry[T]).key)
	}
	return cfg, nil
}

type bindingKey struct{}

type binding struct {
	spec any
	cfg  any // T
}

// Bind implements Runtime.
func (s *Spec[T]) Bind(ctx context.Context, call Call) (context.Context, error) {
	cfg, err := s.decodeCall(call)
	if err != nil {
		return ctx, err
	}
	return s.bind(ctx, cfg), nil
}

func (s *Spec[T]) bind(ctx context.Context, cfg T) context.Context {
	return context.WithValue(ctx, bindingKey{}, binding{spec: s, cfg: cfg})
}

// From returns the config of the call in ctx: the effective config the
// platform sent for the call's tenant, decoded into T, with declared defaults
// for missing keys. Host-audience fields are always zero. Without a configured
// server in ctx (unit tests) it returns T's declared defaults. It panics if
// the server was configured with a different type.
func From[T any](ctx context.Context) T {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok {
		return defaultsOf[T]()
	}
	cfg, ok := b.cfg.(T)
	if !ok {
		panic(fmt.Sprintf("config.From[%s]: the server's config type is %T", reflect.TypeFor[T](), b.cfg))
	}
	return cfg
}

// Schema implements Runtime.
func (s *Spec[T]) Schema() *Schema { return s.decl.schema }

// HasCheck implements Runtime.
func (s *Spec[T]) HasCheck() bool { return s.check != nil }

// Check implements Runtime.
func (s *Spec[T]) Check(ctx context.Context, values *structpb.Struct) (result CheckResult, ok bool) {
	if s.check == nil {
		return CheckResult{}, false
	}
	cfg, err := s.Decode(values)
	if err != nil {
		return CheckResult{Status: CheckFail, Checks: []Check{{
			ID: "config", Label: "Config values", Status: CheckFail, Detail: err.Error(),
		}}}, true
	}
	defer func() {
		if r := recover(); r != nil {
			result = CheckResult{Status: CheckFail, Checks: []Check{{
				ID: "panic", Label: "Config check", Status: CheckFail, Detail: fmt.Sprint(r),
			}}}
		}
	}()
	result = s.check(s.bind(ctx, cfg), cfg)
	if result.Status == "" {
		result.Status = CheckOK
		for _, c := range result.Checks {
			if c.Status == CheckFail {
				result.Status = CheckFail
				break
			}
			if c.Status == CheckWarn {
				result.Status = CheckWarn
			}
		}
	}
	return result, true
}
