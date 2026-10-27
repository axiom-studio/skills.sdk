package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func runJetStream(t *testing.T) *server.Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	return srv
}

type secretsServer struct {
	mu       sync.Mutex
	requests int
	authz    string
	path     string
	body     secretsDoc
}

func (s *secretsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.authz = r.Header.Get("Authorization")
	s.path = r.URL.Path
	_ = json.NewEncoder(w).Encode(s.body)
}

func (s *secretsServer) snapshot() (int, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests, s.authz, s.path
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLoaderLoadsWatchesAndFetchesSecrets(t *testing.T) {
	srv := runJetStream(t)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: Bucket})
	if err != nil {
		t.Fatal(err)
	}
	put := func(v string) {
		t.Helper()
		if _, err := kv.PutString(ctx, "skill-browser", v); err != nil {
			t.Fatal(err)
		}
	}
	// Another Skill's entry must never be read.
	if _, err := kv.PutString(ctx, "skill-other", `{"version":9,"global":{"searchProvider":"tavily"}}`); err != nil {
		t.Fatal(err)
	}

	secrets := &secretsServer{body: secretsDoc{Global: map[string]string{"tavilyApiKey": "k1"}}}
	httpSrv := httptest.NewServer(secrets)
	defer httpSrv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("identity-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore[searchConfig]("skill-browser", nil, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- store.Run(ctx, Source{NATSURL: srv.ClientURL(), SecretsURL: httpSrv.URL + "/sentinel", IdentityTokenFile: tokenFile})
	}()
	bound := store.Bind(ctx, "7")

	// No entry yet: pending on defaults.
	eventually(t, "pending detail", func() bool {
		st := store.Status()
		return st.State == StatePending && st.Detail != ""
	})
	if From[searchConfig](bound).SearchProvider != "yahoo" {
		t.Fatal("defaults not served while pending")
	}

	// Initial load.
	put(`{"version":1,"global":{"searchProvider":"tavily"},"tenants":{"7":{"region":"de"}},"secretsVersion":{"global":1}}`)
	eventually(t, "initial load", func() bool { return store.Status().State == StateLoaded })
	cfg := From[searchConfig](bound)
	if cfg.SearchProvider != "tavily" || cfg.Region != "de" || cfg.TavilyAPIKey.Reveal() != "k1" {
		t.Fatalf("loaded = %+v", cfg)
	}
	n, authz, path := secrets.snapshot()
	if n != 1 || authz != "Bearer identity-token" || path != "/sentinel/internal/skills/skill-browser/config/secrets" {
		t.Fatalf("secrets request n=%d authz=%q path=%q", n, authz, path)
	}

	// Non-secret update: no secrets refetch.
	put(`{"version":2,"global":{"searchProvider":"tavily","maxResults":5},"tenants":{"7":{"region":"de"}},"secretsVersion":{"global":1}}`)
	eventually(t, "watch update", func() bool { return From[searchConfig](bound).MaxResults == 5 })
	if n, _, _ := secrets.snapshot(); n != 1 {
		t.Fatalf("secrets refetched without a version change (%d)", n)
	}

	// Stale republish is ignored.
	put(`{"version":1,"global":{"searchProvider":"yahoo"}}`)
	put(`{"version":3,"global":{"searchProvider":"tavily","maxResults":5},"tenants":{"7":{"region":"de"}},"secretsVersion":{"global":1,"tenants":{"7":1}}}`)
	secrets.mu.Lock()
	secrets.body = secretsDoc{Global: map[string]string{"tavilyApiKey": "k1"}, Tenants: map[string]map[string]string{"7": {"tavilyApiKey": "k7"}}}
	secrets.mu.Unlock()

	// secretsVersion change triggers exactly one fetch.
	eventually(t, "secrets refetch", func() bool { return From[searchConfig](bound).TavilyAPIKey.Reveal() == "k7" })
	if st := store.Status(); st.Version != 3 || st.State != StateLoaded {
		t.Fatalf("status = %+v", st)
	}
	if n, _, _ := secrets.snapshot(); n != 2 {
		t.Fatalf("secrets requests = %d, want 2", n)
	}
	if got := From[searchConfig](store.Bind(ctx, "8")); got.TavilyAPIKey.Reveal() != "k1" || got.Region != "us" {
		t.Fatalf("tenant 8 = %+v", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestLoaderWithoutSourceStaysPending(t *testing.T) {
	store, err := NewStore[searchConfig]("skill-browser", nil, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Run(context.Background(), Source{}); err != nil {
		t.Fatal(err)
	}
	if st := store.Status(); st.State != StatePending || st.Detail == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestLoaderKeepsPendingWhenSecretsFail(t *testing.T) {
	store, err := NewStore[searchConfig]("skill-browser", nil, Scopes(Global, Tenant))
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer httpSrv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tokenFile, []byte("t"), 0o600)
	l := &loader[searchConfig]{store: store, secrets: &secretsDoc{},
		src: Source{SecretsURL: httpSrv.URL, IdentityTokenFile: tokenFile, HTTPClient: httpSrv.Client()}}
	l.doc = &document{Version: 1, Global: rawMap(t, map[string]any{"maxResults": 7}), SecretsVersion: secretsVersion{Global: 1}}
	if err := l.apply(context.Background()); err == nil {
		t.Fatal("expected fetch error")
	}
	st := store.Status()
	if st.State != StatePending || st.Detail == "" {
		t.Fatalf("status = %+v", st)
	}
	if From[searchConfig](store.Bind(context.Background(), "")).MaxResults != 7 {
		t.Fatal("non-secret values should still apply")
	}
}
