package identity

import (
	"strings"
	"unicode"

	"github.com/nodera/nodera/internal/platform/apierr"
)

func validateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return apierr.Validation("email is required")
	}
	at := strings.Index(email, "@")
	if at <= 0 || at == len(email)-1 || strings.Contains(email[at+1:], "@") {
		return apierr.Validation("email is not a valid address")
	}
	return nil
}

// validatePassword enforces a minimum-strength baseline. This is
// intentionally simple (length + character variety) rather than a fixed
// composition rule NIST now advises against — a long passphrase is accepted.
func validatePassword(password string) error {
	if len(password) < 12 {
		return apierr.Validation("password must be at least 12 characters")
	}
	if len(password) > 256 {
		return apierr.Validation("password must be at most 256 characters")
	}

	var hasLetter, hasOther bool
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else {
			hasOther = true
		}
	}
	if !hasLetter || !hasOther {
		return apierr.Validation("password must contain letters and at least one number or symbol")
	}
	return nil
}
