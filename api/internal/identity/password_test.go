package identity

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple 9")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	ok, err := verifyPassword("correct horse battery staple 9", hash)
	if err != nil {
		t.Fatalf("verifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected correct password to verify")
	}

	ok, err = verifyPassword("wrong password entirely 9", hash)
	if err != nil {
		t.Fatalf("verifyPassword: %v", err)
	}
	if ok {
		t.Fatal("expected incorrect password to fail verification")
	}
}

func TestHashPasswordProducesUniqueSalts(t *testing.T) {
	h1, err := hashPassword("same password used twice 1")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	h2, err := hashPassword("same password used twice 1")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected two hashes of the same password to differ due to random salts")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"too short", "short1", true},
		{"letters only", "onlylettersnodigits", true},
		{"digits only", "123456789012", true},
		{"valid passphrase", "correct horse battery staple 9", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePassword(c.pw)
			if (err != nil) != c.wantErr {
				t.Fatalf("validatePassword(%q) error = %v, wantErr %v", c.pw, err, c.wantErr)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	cases := []struct {
		email   string
		wantErr bool
	}{
		{"user@example.com", false},
		{"", true},
		{"not-an-email", true},
		{"a@b@c.com", true},
		{"@example.com", true},
	}
	for _, c := range cases {
		if err := validateEmail(c.email); (err != nil) != c.wantErr {
			t.Errorf("validateEmail(%q) error = %v, wantErr %v", c.email, err, c.wantErr)
		}
	}
}
