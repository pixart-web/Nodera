package local

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/nodera/nodera/internal/providers"
)

// DNS is an in-process authoritative zone store (no registrar involved).
// CheckPropagation resolves against the in-process zone, which is exactly
// "propagated" by definition; a real provider (Cloudflare/Hetzner) checks
// public resolvers instead.
type DNS struct {
	mu    sync.Mutex
	zones map[string][]providers.DNSRecord
	seq   int
}

func NewDNS() *DNS { return &DNS{zones: map[string][]providers.DNSRecord{}} }

func (d *DNS) EnsureZone(_ context.Context, domain string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.zones[domain]; !ok {
		d.zones[domain] = nil
	}
	return nil
}
func (d *DNS) ListRecords(_ context.Context, domain string) ([]providers.DNSRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, ok := d.zones[domain]
	if !ok {
		return nil, providers.ErrNotFound
	}
	return append([]providers.DNSRecord(nil), z...), nil
}
func (d *DNS) UpsertRecord(_ context.Context, domain string, rec providers.DNSRecord) (providers.DNSRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, ok := d.zones[domain]
	if !ok {
		return providers.DNSRecord{}, providers.ErrNotFound
	}
	if rec.Type == "A" && net.ParseIP(rec.Value) == nil {
		return providers.DNSRecord{}, fmt.Errorf("invalid A record value %q", rec.Value)
	}
	for i, r := range z {
		if r.Type == rec.Type && r.Name == rec.Name && r.Value == rec.Value {
			rec.ID = r.ID
			z[i] = rec
			return rec, nil
		}
	}
	d.seq++
	rec.ID = fmt.Sprintf("local-rec-%d", d.seq)
	d.zones[domain] = append(z, rec)
	return rec, nil
}
func (d *DNS) DeleteRecord(_ context.Context, domain, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	z := d.zones[domain]
	for i, r := range z {
		if r.ID == id {
			d.zones[domain] = append(z[:i], z[i+1:]...)
			return nil
		}
	}
	return nil
}
func (d *DNS) CheckPropagation(_ context.Context, domain string, rec providers.DNSRecord) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.zones[domain] {
		if r.Type == rec.Type && r.Name == rec.Name && r.Value == rec.Value {
			return true, nil
		}
	}
	return false, nil
}
