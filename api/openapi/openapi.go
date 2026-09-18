// Package openapi embeds Nodera's OpenAPI 3.0 spec so the compiled binary
// always serves exactly the spec it shipped with. Hand-maintained for now
// (docs/ROADMAP.md tracks generating it from code instead) — keep
// openapi.json in sync with cmd/server/router.go when routes change.
package openapi

import _ "embed"

//go:embed openapi.json
var Spec []byte
