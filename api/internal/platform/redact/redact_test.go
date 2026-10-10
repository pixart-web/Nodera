package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsCredentialShapes(t *testing.T) {
	cases := map[string]string{
		"connecting with password=hunter2 to db":                                 "hunter2",
		`{"api_key":"sk-live-abcdef123456"}`:                                     "sk-live-abcdef123456",
		"Authorization: Bearer eyJhbGciOi.abc.def":                               "eyJhbGciOi",
		"token ndr_enr_AbCdEfGhIjKlMnOpQrStUvWx used":                            "AbCdEfGhIjKlMnOpQrStUvWx",
		"mysql://root:s3cr3tpass@db:3306/wp":                                     "s3cr3tpass",
		"key AKIAIOSFODNN7EXAMPLE leaked":                                        "AKIAIOSFODNN7EXAMPLE",
		"gh ghp_abcdefghijklmnopqrstuvwxyz0123456789 now":                        "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEE\n-----END EC PRIVATE KEY-----": "MHcCAQEE",
		"client_secret: 'topsecretvalue'":                                        "topsecretvalue",
	}
	for in, secret := range cases {
		if out := String(in); strings.Contains(out, secret) {
			t.Errorf("secret %q survived in %q -> %q", secret, in, out)
		}
	}
	if got := String("deployed release 42 to production in 3.2s"); got != "deployed release 42 to production in 3.2s" {
		t.Errorf("harmless text was altered: %q", got)
	}
}
