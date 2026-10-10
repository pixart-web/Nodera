package local

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/nodera/nodera/internal/providers"
)

// Certs issues REAL X.509 certificates signed by a locally generated CA. They
// are valid X.509 but not publicly trusted — the Let's Encrypt provider
// (future) implements the same interface for browser-trusted certificates.
type Certs struct {
	mu       sync.Mutex
	ca       *x509.Certificate
	caKey    *ecdsa.PrivateKey
	Validity time.Duration
	revoked  map[string]bool
}

func NewCerts() (*Certs, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Nodera Local CA", Organization: []string{"Nodera (local development)"}},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Certs{ca: ca, caKey: key, Validity: 90 * 24 * time.Hour, revoked: map[string]bool{}}, nil
}

func (c *Certs) Name() string { return "local-ca" }

func (c *Certs) Issue(_ context.Context, req providers.IssueRequest) (providers.CertBundle, error) {
	if len(req.Domains) == 0 {
		return providers.CertBundle{}, errors.New("at least one domain is required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return providers.CertBundle{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return providers.CertBundle{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: req.Domains[0]}, DNSNames: req.Domains,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(c.Validity),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	c.mu.Lock()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.ca, &key.PublicKey, c.caKey)
	c.mu.Unlock()
	if err != nil {
		return providers.CertBundle{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return providers.CertBundle{}, err
	}
	return providers.CertBundle{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		Issuer:  c.ca.Subject.CommonName, Serial: serial.Text(16), NotBefore: tmpl.NotBefore, NotAfter: tmpl.NotAfter,
	}, nil
}
func (c *Certs) Renew(ctx context.Context, req providers.IssueRequest) (providers.CertBundle, error) {
	return c.Issue(ctx, req)
}
func (c *Certs) Revoke(_ context.Context, serial string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoked[serial] = true
	return nil
}
func (c *Certs) Inspect(_ context.Context, certPEM []byte) (providers.CertInfo, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return providers.CertInfo{}, errors.New("not a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return providers.CertInfo{}, fmt.Errorf("parse certificate: %w", err)
	}
	return providers.CertInfo{Issuer: cert.Issuer.CommonName, Serial: cert.SerialNumber.Text(16), Subject: cert.Subject.CommonName,
		DNSNames: cert.DNSNames, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}, nil
}
