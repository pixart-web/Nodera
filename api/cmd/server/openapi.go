package main

import (
	"net/http"

	"github.com/nodera/nodera/openapi"
)

// handleOpenAPISpec serves the raw spec — unauthenticated, like /health,
// since API documentation isn't sensitive.
func (d apiDeps) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openapi.Spec)
}

// handleAPIDocs serves a minimal Swagger UI page (loaded from a CDN, no
// bundling needed) pointed at /openapi.json.
func (d apiDeps) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(apiDocsHTML)
}

var apiDocsHTML = []byte(`<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Nodera API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => SwaggerUIBundle({ url: "/openapi.json", dom_id: "#swagger-ui" });
  </script>
</body>
</html>
`)
