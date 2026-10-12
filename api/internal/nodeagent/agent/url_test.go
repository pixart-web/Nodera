package agent

import "testing"

func TestValidateAPIURL(t *testing.T) {
	for _, ok := range []string{"https://nodera.example.com", "https://nodera.example.com:8443", "http://api:8080", "http://localhost:8080", "http://127.0.0.1:8080"} {
		if err := ValidateAPIURL(ok); err != nil {
			t.Errorf("%s should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://nodera.example.com", "http://203.0.113.5:8080", "ftp://x", "nodera.example.com", "https://x.example.com/api", ""} {
		if err := ValidateAPIURL(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}
