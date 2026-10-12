package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func TestRequestSignatureBindsEverything(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	body := []byte(`{"a":1}`)
	h := SignRequest(priv, "ag1", "POST", "/agent/v1/poll", body, now, "nonce-0123456789ab")
	get := func(k string) string { return h[k] }
	if err := VerifyRequest(pub, get, "POST", "/agent/v1/poll", body, now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for name, f := range map[string]func() error{
		"body":   func() error { return VerifyRequest(pub, get, "POST", "/agent/v1/poll", []byte(`{"a":2}`), now) },
		"path":   func() error { return VerifyRequest(pub, get, "POST", "/agent/v1/heartbeat", body, now) },
		"method": func() error { return VerifyRequest(pub, get, "GET", "/agent/v1/poll", body, now) },
		"stale":  func() error { return VerifyRequest(pub, get, "POST", "/agent/v1/poll", body, now.Add(5*time.Minute)) },
	} {
		if f() == nil {
			t.Errorf("%s change must invalidate the signature", name)
		}
	}
}

func TestAllowlistValidators(t *testing.T) {
	ok := map[string]string{
		"docker.create":    `{"name":"web","image":"nginx:1.27"}`,
		"filesystem.write": `{"path":"sites/a/index.php","content_b64":"aGk="}`,
		"service.health":   `{"kind":"http","target":"https://example.com"}`,
	}
	for op, p := range ok {
		if err := ValidateParams(op, json.RawMessage(p)); err != nil {
			t.Errorf("%s should validate: %v", op, err)
		}
	}
	bad := map[string]string{
		"shell.exec":       `{}`,
		"filesystem.read":  `{"path":"a/../../b"}`,
		"filesystem.write": `{"path":"/abs"}`,
		"docker.create":    `{"name":"x","image":"-v /:/host"}`,
		"docker.stop":      `{"name":"$(id)"}`,
		"service.health":   `{"kind":"http","target":"file:///etc/passwd"}`,
	}
	for op, p := range bad {
		if err := ValidateParams(op, json.RawMessage(p)); err == nil {
			t.Errorf("%s %s should be rejected", op, p)
		}
	}
}

func TestWorkloadUserNeverRoot(t *testing.T) {
	ok := func(u string) error {
		return ValidateParams("docker.create", json.RawMessage(`{"name":"w","image":"nginx","user":"`+u+`"}`))
	}
	for _, good := range []string{"10001", "10001:10001", "33:33"} {
		if err := ok(good); err != nil {
			t.Errorf("%q should be accepted: %v", good, err)
		}
	}
	for _, bad := range []string{"0", "0:0", "00", "root", "1000:root", "0:1000", "-1"} {
		if err := ok(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if err := ValidateParams("docker.create", json.RawMessage(`{"name":"w","image":"nginx"}`)); err != nil {
		t.Errorf("no user keeps the image default: %v", err)
	}
}

func TestDatabaseOpsValidateIdentifiers(t *testing.T) {
	if err := ValidateParams("database.ensure", json.RawMessage(`{"name":"wp_a","user":"u_a","password":"abcdefghijklmnop12"}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"name":"a b","user":"u","password":"abcdefghijklmnop12"}`,
		`{"name":"a","user":"u'; DROP USER x;--","password":"abcdefghijklmnop12"}`,
		`{"name":"a","user":"u","password":"short"}`,
		`{"name":"a","user":"u","password":"has space in it 123456"}`,
	} {
		if err := ValidateParams("database.ensure", json.RawMessage(bad)); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if err := ValidateParams("database.dump", json.RawMessage(`{"name":"a","path":"../x"}`)); err == nil {
		t.Error("dump path traversal must be rejected")
	}
}
