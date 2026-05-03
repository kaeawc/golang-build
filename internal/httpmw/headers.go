package httpmw

// Standard header names used by middleware in this package. Centralized so
// callers can reference them by constant instead of stringly-typed literals.
const (
	HeaderRequestID     = "X-Request-ID"
	HeaderForwardedFor  = "X-Forwarded-For"
	HeaderRealIP        = "X-Real-IP"
	HeaderOrigin        = "Origin"
	HeaderVary          = "Vary"
	HeaderHSTS          = "Strict-Transport-Security"
	HeaderFrameOptions  = "X-Frame-Options"
	HeaderContentType   = "X-Content-Type-Options"
	HeaderReferrer      = "Referrer-Policy"
	HeaderCSP           = "Content-Security-Policy"
	HeaderPermissions   = "Permissions-Policy"
	HeaderAllowOrigin   = "Access-Control-Allow-Origin"
	HeaderAllowMethods  = "Access-Control-Allow-Methods"
	HeaderAllowHeaders  = "Access-Control-Allow-Headers"
	HeaderAllowCreds    = "Access-Control-Allow-Credentials"
	HeaderExposeHeaders = "Access-Control-Expose-Headers"
	HeaderMaxAge        = "Access-Control-Max-Age"
	HeaderRequestMethod = "Access-Control-Request-Method"
)
