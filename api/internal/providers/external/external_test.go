package external

import (
	"context"
	"errors"
	"testing"

	"github.com/nodera/nodera/internal/providers"
)

// The prepared adapters must never report success: a misconfigured deployment
// has to fail loudly instead of pretending an external call happened.
func TestPreparedAdaptersAlwaysFailHonestly(t *testing.T) {
	ctx := context.Background()
	var errs []error
	dns := CloudflareDNS{APIToken: "x"}
	errs = append(errs, dns.EnsureZone(ctx, "a.com"))
	_, e := dns.UpsertRecord(ctx, "a.com", providers.DNSRecord{})
	errs = append(errs, e)
	le := LetsEncrypt{Email: "a@b.c"}
	_, e = le.Issue(ctx, providers.IssueRequest{Domains: []string{"a.com"}})
	errs = append(errs, e, le.Revoke(ctx, "1"))
	h := HetznerNodes{APIToken: "x"}
	_, e = h.Describe(ctx, "1")
	errs = append(errs, e, h.Reboot(ctx, "1"))
	gh := GitHub{Token: "x"}
	_, e = gh.ListRepositories(ctx)
	errs = append(errs, e)
	_, e = gh.Fetch(ctx, "o/r", "main", nil, "x")
	errs = append(errs, e)
	for i, err := range errs {
		if !errors.Is(err, providers.ErrUnavailable) {
			t.Errorf("call %d: want ErrUnavailable, got %v", i, err)
		}
	}
}
