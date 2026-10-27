package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Bucket is the JetStream KV bucket holding one entry per Skill, keyed by
// Skill ID.
const Bucket = "skill-config"

// Environment the platform sets on every Skill pod.
const (
	EnvNATSURL           = "AXIOM_SKILL_CONFIG_NATS_URL"
	EnvNATSCreds         = "AXIOM_SKILL_CONFIG_NATS_CREDS"  // path to a .creds file
	EnvNATSDomain        = "AXIOM_SKILL_CONFIG_NATS_DOMAIN" // optional JetStream domain
	EnvSecretsURL        = "AXIOM_SKILL_CONFIG_SECRETS_URL" // Sentinel base URL
	EnvIdentityTokenFile = "AXIOM_SKILL_IDENTITY_TOKEN_FILE"
)

// Source says where a Skill's config comes from.
type Source struct {
	NATSURL    string
	NATSCreds  string
	NATSDomain string
	SecretsURL string
	// IdentityTokenFile holds the Skill's workload identity bearer token. It
	// is re-read on every secrets fetch so rotated tokens are picked up.
	IdentityTokenFile string
	HTTPClient        *http.Client
}

// SourceFromEnv reads the Source from the platform's environment.
func SourceFromEnv() Source {
	return Source{
		NATSURL:           os.Getenv(EnvNATSURL),
		NATSCreds:         os.Getenv(EnvNATSCreds),
		NATSDomain:        os.Getenv(EnvNATSDomain),
		SecretsURL:        os.Getenv(EnvSecretsURL),
		IdentityTokenFile: os.Getenv(EnvIdentityTokenFile),
	}
}

// document is a Skill's KV entry. It never carries secret or host values.
type document struct {
	Version        uint64                                `json:"version"`
	Global         map[string]json.RawMessage            `json:"global"`
	Tenants        map[string]map[string]json.RawMessage `json:"tenants"`
	SecretsVersion secretsVersion                        `json:"secretsVersion"`
}

type secretsVersion struct {
	Global  uint64            `json:"global"`
	Tenants map[string]uint64 `json:"tenants"`
}

func (v secretsVersion) zero() bool {
	if v.Global != 0 {
		return false
	}
	for _, n := range v.Tenants {
		if n != 0 {
			return false
		}
	}
	return true
}

// secretsDoc is Sentinel's secrets response: secret fields only.
type secretsDoc struct {
	Global  map[string]string            `json:"global"`
	Tenants map[string]map[string]string `json:"tenants"`
}

// Retry backoff bounds.
const (
	retryMin = time.Second
	retryMax = 30 * time.Second
)

// Run implements Runtime. Without a NATS URL it returns at once and the Skill
// keeps running on declared defaults with config state pending.
func (s *Store[T]) Run(ctx context.Context, src Source) error {
	if src.NATSURL == "" {
		s.setDetail("config source not configured; running on declared defaults")
		return nil
	}
	opts := []nats.Option{
		nats.Name("skill-config:" + s.skillID),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	}
	if src.NATSCreds != "" {
		opts = append(opts, nats.UserCredentials(src.NATSCreds))
	}
	nc, err := nats.Connect(src.NATSURL, opts...)
	if err != nil {
		s.setDetail("connect config source: " + err.Error())
		return err
	}
	defer nc.Close()
	var js jetstream.JetStream
	if src.NATSDomain != "" {
		js, err = jetstream.NewWithDomain(nc, src.NATSDomain)
	} else {
		js, err = jetstream.New(nc)
	}
	if err != nil {
		return err
	}
	l := &loader[T]{store: s, src: src, secrets: &secretsDoc{}}
	if l.src.HTTPClient == nil {
		l.src.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	backoff := retryMin
	for {
		err := l.watch(ctx, js)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.setDetail(err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, retryMax)
	}
}

// loader is the single goroutine's view of the last entry and secrets.
type loader[T any] struct {
	store *Store[T]
	src   Source

	doc            *document
	secrets        *secretsDoc
	secretsFetched *secretsVersion // version the held secrets belong to
}

func (l *loader[T]) watch(ctx context.Context, js jetstream.JetStream) error {
	kv, err := js.KeyValue(ctx, Bucket)
	if err != nil {
		return fmt.Errorf("open config bucket: %w", err)
	}
	w, err := kv.Watch(ctx, l.store.skillID)
	if err != nil {
		return fmt.Errorf("watch config: %w", err)
	}
	defer func() { _ = w.Stop() }()

	var retry <-chan time.Time
	backoff := retryMin
	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-w.Updates():
			if !ok {
				return errors.New("config watch closed")
			}
			if entry == nil { // caught up with existing entries
				if l.doc == nil {
					l.store.setDetail("no config published yet; running on declared defaults")
				}
				continue
			}
			var doc document
			switch entry.Operation() {
			case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
				// The entry was removed: back to declared defaults.
			default:
				if err := json.Unmarshal(entry.Value(), &doc); err != nil {
					l.store.setDetail("invalid config entry: " + err.Error())
					continue
				}
				if l.doc != nil && doc.Version < l.doc.Version {
					continue // stale republish
				}
			}
			l.doc = &doc
			retry, backoff = nil, retryMin
			if err := l.apply(ctx); err != nil {
				retry = time.After(backoff)
			}
		case <-retry:
			backoff = min(backoff*2, retryMax)
			retry = nil
			if err := l.apply(ctx); err != nil {
				retry = time.After(backoff)
			}
		}
	}
}

// apply fetches secrets if their version changed, then swaps in a new
// snapshot. Non-secret values apply even when the fetch fails; the state stays
// as it was until secrets are current.
func (l *loader[T]) apply(ctx context.Context) error {
	s := l.store
	var fetchErr error
	if want := l.doc.SecretsVersion; want.zero() {
		l.secrets, l.secretsFetched = &secretsDoc{}, &want
	} else if l.secretsFetched == nil || !reflect.DeepEqual(*l.secretsFetched, want) {
		secrets, err := l.fetchSecrets(ctx)
		if err != nil {
			fetchErr = fmt.Errorf("fetch config secrets: %w", err)
		} else {
			l.secrets, l.secretsFetched = secrets, &want
		}
	}

	state := StateLoaded
	if fetchErr != nil {
		state = s.snap.Load().state
	}
	var problems []string
	s.snap.Store(s.build(state, l.doc, l.secrets, &problems))
	switch {
	case fetchErr != nil:
		s.setDetail(fetchErr.Error())
	case len(problems) > 0:
		s.setDetail("ignored invalid values: " + strings.Join(problems, "; "))
	default:
		s.setDetail("")
	}
	return fetchErr
}

func (l *loader[T]) fetchSecrets(ctx context.Context) (*secretsDoc, error) {
	if l.src.SecretsURL == "" {
		return nil, fmt.Errorf("%s is not set", EnvSecretsURL)
	}
	if l.src.IdentityTokenFile == "" {
		return nil, fmt.Errorf("%s is not set", EnvIdentityTokenFile)
	}
	token, err := os.ReadFile(l.src.IdentityTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read identity token: %w", err)
	}
	endpoint, err := url.JoinPath(l.src.SecretsURL, "internal", "skills", l.store.skillID, "config", "secrets")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	resp, err := l.src.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("secrets endpoint returned %s", resp.Status)
	}
	var doc secretsDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode secrets: %w", err)
	}
	return &doc, nil
}
