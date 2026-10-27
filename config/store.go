package config

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
)

// State is the load state of a Skill's config.
type State string

const (
	// StatePending: no config from the platform yet; declared defaults apply.
	StatePending State = "pending"
	// StateLoaded: the platform's config (and secrets) are in effect.
	StateLoaded State = "loaded"
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

// Status describes the loaded config.
type Status struct {
	State   State
	Version uint64
	Detail  string
}

// Runtime is the non-generic face of a Store, used by the gRPC server.
type Runtime interface {
	// Bind returns ctx carrying this config and the authenticated tenant of
	// the call. Only the SDK's request path calls it.
	Bind(ctx context.Context, tenantID string) context.Context
	Schema() *Schema
	Status() Status
	// Check runs the declared config check against the tenant's effective
	// config; ok is false when no check is declared.
	Check(ctx context.Context, tenantID string) (result CheckResult, ok bool)
	// Run loads and watches the platform's config until ctx ends.
	Run(ctx context.Context, src Source) error
}

// Store holds a Skill's typed config snapshots.
type Store[T any] struct {
	skillID string
	decl    *declaration
	check   func(context.Context, T) CheckResult
	snap    atomic.Pointer[snapshot[T]]

	mu     sync.Mutex
	detail string
}

type snapshot[T any] struct {
	state   State
	version uint64
	global  T
	tenants map[string]T
}

// NewStore validates T's declaration and returns a store serving declared
// defaults until Run loads the platform's config. check may be nil.
func NewStore[T any](skillID string, check func(context.Context, T) CheckResult, opts ...Option) (*Store[T], error) {
	d, err := declare(reflect.TypeFor[T](), opts)
	if err != nil {
		return nil, err
	}
	s := &Store[T]{skillID: skillID, decl: d, check: check}
	s.snap.Store(s.build(StatePending, &document{}, &secretsDoc{}, nil))
	return s, nil
}

type bindingKey struct{}

type binding struct {
	store  any
	tenant string
}

// Bind implements Runtime.
func (s *Store[T]) Bind(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, bindingKey{}, binding{store: s, tenant: tenantID})
}

// From returns the effective config (defaults, then global, then the tenant's
// overrides) for the authenticated tenant of the call in ctx. Host-audience
// fields are always zero. Without a configured server in ctx (unit tests) it
// returns T's declared defaults. It panics if the server was configured with a
// different type.
func From[T any](ctx context.Context) T {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok {
		return defaultsOf[T]()
	}
	s, ok := b.store.(*Store[T])
	if !ok {
		panic(fmt.Sprintf("config.From[%s]: the server's config type is %T", reflect.TypeFor[T](), b.store))
	}
	return s.get(b.tenant)
}

func (s *Store[T]) get(tenantID string) T {
	snap := s.snap.Load()
	if t, ok := snap.tenants[tenantID]; ok && tenantID != "" {
		return t
	}
	return snap.global
}

// Schema implements Runtime.
func (s *Store[T]) Schema() *Schema { return s.decl.schema }

// Status implements Runtime.
func (s *Store[T]) Status() Status {
	snap := s.snap.Load()
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{State: snap.state, Version: snap.version, Detail: s.detail}
}

func (s *Store[T]) setDetail(detail string) {
	s.mu.Lock()
	s.detail = detail
	s.mu.Unlock()
}

// Check implements Runtime.
func (s *Store[T]) Check(ctx context.Context, tenantID string) (result CheckResult, ok bool) {
	if s.check == nil {
		return CheckResult{}, false
	}
	defer func() {
		if r := recover(); r != nil {
			result = CheckResult{Status: CheckFail, Checks: []Check{{
				ID: "panic", Label: "Config check", Status: CheckFail, Detail: fmt.Sprint(r),
			}}}
		}
	}()
	result = s.check(s.Bind(ctx, tenantID), s.get(tenantID))
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

// layers are the values that apply on top of declared defaults, lowest first.
type layers struct {
	global       map[string]json.RawMessage
	tenant       map[string]json.RawMessage
	secretGlobal map[string]string
	secretTenant map[string]string
}

// apply writes defaults and the layers into dst and returns the keys whose
// pushed values were invalid (and therefore ignored).
func (d *declaration) apply(dst reflect.Value, l *layers) []string {
	if l == nil {
		l = &layers{}
	}
	var problems []string
	for _, f := range d.fields {
		if f.Audience == AudienceHost {
			continue
		}
		v := f.effectiveV
		if f.Type == TypeSecret {
			if s := l.secretGlobal[f.Key]; f.inGlobal && s != "" {
				v = s
			}
			if s := l.secretTenant[f.Key]; f.inTenant && s != "" {
				v = s
			}
		} else {
			for _, layer := range []struct {
				values  map[string]json.RawMessage
				allowed bool
			}{{l.global, f.inGlobal}, {l.tenant, f.inTenant}} {
				raw, ok := layer.values[f.Key]
				if !ok || !layer.allowed || string(raw) == "null" {
					continue
				}
				parsed, err := f.decode(raw)
				if err != nil {
					problems = append(problems, err.Error())
					continue
				}
				v = parsed
			}
		}
		if v != nil {
			f.set(dst, v)
		}
	}
	return problems
}

func (s *Store[T]) build(state State, doc *document, secrets *secretsDoc, problems *[]string) *snapshot[T] {
	snap := &snapshot[T]{state: state, version: doc.Version, tenants: map[string]T{}}
	collect := func(p []string) {
		if problems != nil {
			*problems = append(*problems, p...)
		}
	}
	collect(s.decl.apply(reflect.ValueOf(&snap.global).Elem(), &layers{
		global: doc.Global, secretGlobal: secrets.Global,
	}))
	ids := map[string]struct{}{}
	for id := range doc.Tenants {
		ids[id] = struct{}{}
	}
	for id := range secrets.Tenants {
		ids[id] = struct{}{}
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		if id == "" {
			continue
		}
		var t T
		collect(s.decl.apply(reflect.ValueOf(&t).Elem(), &layers{
			global: doc.Global, tenant: doc.Tenants[id],
			secretGlobal: secrets.Global, secretTenant: secrets.Tenants[id],
		}))
		snap.tenants[id] = t
	}
	return snap
}
