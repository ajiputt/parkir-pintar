package middleware

import "net/http"

// SecurityHeaders — tambah HTTP security headers di response.
//
// Best-practice headers per OWASP Secure Headers Project:
//   - HSTS                  : force HTTPS di future visit (browser cache 1 tahun)
//   - X-Content-Type-Options: cegah MIME sniffing
//   - X-Frame-Options       : cegah clickjacking
//   - Referrer-Policy       : limit referrer info ke third-party
//   - Permissions-Policy    : disable browser features yang tidak diperlukan
//
// CSP (Content-Security-Policy) tidak di-set di sini karena:
//   - /docs (Swagger UI) butuh load script dari cdn.jsdelivr.net
//   - Production: configure CSP per-route (mis. /docs vs /v1/*)
//
// Reference:
//
//	https://owasp.org/www-project-secure-headers/
//	https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// HSTS — force HTTPS untuk 1 tahun. Hanya set kalau request di HTTPS.
		// (Behind LB yang terminate TLS, X-Forwarded-Proto = https.)
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		// Cegah MIME sniffing.
		h.Set("X-Content-Type-Options", "nosniff")

		// Cegah embedding di iframe (clickjacking protection).
		h.Set("X-Frame-Options", "DENY")

		// Limit referrer info — same-origin penuh, cross-origin cuma origin.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Disable browser features yang tidak diperlukan parking app.
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")

		// Cross-Origin-Resource-Policy — cegah cross-site script load resource.
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}
