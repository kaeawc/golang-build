package httpmw

import "net/http"

// SecureHeadersConfig controls SecureHeaders. Zero/empty values omit the
// corresponding header.
type SecureHeadersConfig struct {
	// HSTS is the value of Strict-Transport-Security. Default empty (omits
	// the header). Use "max-age=63072000; includeSubDomains" or similar
	// for HTTPS deployments. Setting this on plain HTTP traffic is harmless
	// (browsers ignore it).
	HSTS string
	// FrameOptions is the value of X-Frame-Options. Default "DENY".
	// Set to empty to omit; "SAMEORIGIN" to allow same-origin framing.
	FrameOptions string
	// ContentTypeOptions is the value of X-Content-Type-Options. Default
	// "nosniff".
	ContentTypeOptions string
	// ReferrerPolicy is the value of Referrer-Policy. Default
	// "strict-origin-when-cross-origin".
	ReferrerPolicy string
	// ContentSecurityPolicy is the value of Content-Security-Policy.
	// Default empty — set per app.
	ContentSecurityPolicy string
	// PermissionsPolicy is the value of Permissions-Policy. Default empty.
	PermissionsPolicy string
}

// DefaultSecureHeaders is a reasonable set of defaults for an HTTPS service.
// HSTS is intentionally empty so it's not set on accident in development;
// override with your production policy explicitly.
var DefaultSecureHeaders = SecureHeadersConfig{
	FrameOptions:       "DENY",
	ContentTypeOptions: "nosniff",
	ReferrerPolicy:     "strict-origin-when-cross-origin",
}

// SecureHeaders middleware sets a curated set of security response headers.
// Empty fields in cfg are omitted. The response writer is not buffered;
// headers must be written before any handler call to WriteHeader.
func SecureHeaders(cfg SecureHeadersConfig) Middleware {
	headers := buildHeaders(cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for k, v := range headers {
				h.Set(k, v)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func buildHeaders(cfg SecureHeadersConfig) map[string]string {
	out := map[string]string{}
	if cfg.HSTS != "" {
		out[HeaderHSTS] = cfg.HSTS
	}
	if cfg.FrameOptions != "" {
		out[HeaderFrameOptions] = cfg.FrameOptions
	}
	if cfg.ContentTypeOptions != "" {
		out[HeaderContentType] = cfg.ContentTypeOptions
	}
	if cfg.ReferrerPolicy != "" {
		out[HeaderReferrer] = cfg.ReferrerPolicy
	}
	if cfg.ContentSecurityPolicy != "" {
		out[HeaderCSP] = cfg.ContentSecurityPolicy
	}
	if cfg.PermissionsPolicy != "" {
		out[HeaderPermissions] = cfg.PermissionsPolicy
	}
	return out
}
