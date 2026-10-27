package config

import (
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Secret holds a secret config value. It never prints, logs or marshals the
// value; call Reveal to use it. The value sits behind a pointer so that even
// reflection-based printing of an unexported field shows only an address.
type Secret struct {
	p *string
}

// NewSecret wraps a value, for tests and local tooling.
func NewSecret(value string) Secret {
	if value == "" {
		return Secret{}
	}
	return Secret{p: &value}
}

// Reveal returns the secret value.
func (s Secret) Reveal() string {
	if s.p == nil {
		return ""
	}
	return *s.p
}

// IsSet reports whether the secret has a non-empty value.
func (s Secret) IsSet() bool { return s.p != nil && *s.p != "" }

// String implements fmt.Stringer.
func (Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return "config.Secret(" + redacted + ")" }

// Format implements fmt.Formatter, so every verb is redacted.
func (s Secret) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('#') {
		_, _ = f.Write([]byte(s.GoString()))
		return
	}
	_, _ = f.Write([]byte(redacted))
}

// MarshalJSON implements json.Marshaler.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText implements encoding.TextMarshaler.
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
