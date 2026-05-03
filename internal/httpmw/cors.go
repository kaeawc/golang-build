package httpmw

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig controls the CORS middleware. Zero values disable the
// corresponding feature.
type CORSConfig struct {
	// AllowedOrigins is the list of origins permitted to make requests.
	// "*" means allow any. Empty means CORS is effectively disabled
	// (no Access-Control-Allow-Origin header is set).
	AllowedOrigins []string
	// AllowedMethods defaults to GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowedMethods []string
	// AllowedHeaders defaults to Content-Type, Authorization, X-Request-ID.
	AllowedHeaders []string
	// ExposedHeaders are the response headers the browser may surface to
	// JavaScript. Default empty.
	ExposedHeaders []string
	// AllowCredentials sets Access-Control-Allow-Credentials. Cannot be
	// combined with AllowedOrigins=["*"].
	AllowCredentials bool
	// MaxAge sets Access-Control-Max-Age in seconds (preflight cache).
	MaxAge int
}

// CORS middleware handles preflight OPTIONS requests and adds CORS headers
// to actual responses.
func CORS(cfg CORSConfig) Middleware {
	allowed := corsRules{
		origins:          buildOriginSet(cfg.AllowedOrigins),
		methods:          strings.Join(defaultIfEmpty(cfg.AllowedMethods, "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"), ", "),
		reqHeaders:       strings.Join(defaultIfEmpty(cfg.AllowedHeaders, "Content-Type", "Authorization", "X-Request-ID"), ", "),
		exposed:          strings.Join(cfg.ExposedHeaders, ", "),
		allowCredentials: cfg.AllowCredentials,
		maxAge:           cfg.MaxAge,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get(HeaderOrigin)
			if origin != "" {
				allowed.applyTo(w.Header(), origin)
			}
			if r.Method == http.MethodOptions && r.Header.Get(HeaderRequestMethod) != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type corsRules struct {
	origins          *originSet
	methods          string
	reqHeaders       string
	exposed          string
	allowCredentials bool
	maxAge           int
}

func (c corsRules) applyTo(h http.Header, origin string) {
	if !c.origins.allows(origin) {
		return
	}
	if c.origins.wildcard && !c.allowCredentials {
		h.Set(HeaderAllowOrigin, "*")
	} else {
		h.Set(HeaderAllowOrigin, origin)
		h.Add(HeaderVary, "Origin")
	}
	if c.allowCredentials {
		h.Set(HeaderAllowCreds, "true")
	}
	if c.exposed != "" {
		h.Set(HeaderExposeHeaders, c.exposed)
	}
	if c.methods != "" {
		h.Set(HeaderAllowMethods, c.methods)
	}
	if c.reqHeaders != "" {
		h.Set(HeaderAllowHeaders, c.reqHeaders)
	}
	if c.maxAge > 0 {
		h.Set(HeaderMaxAge, strconv.Itoa(c.maxAge))
	}
}

type originSet struct {
	wildcard bool
	exact    map[string]struct{}
}

func buildOriginSet(origins []string) *originSet {
	s := &originSet{exact: map[string]struct{}{}}
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "*" {
			s.wildcard = true
			continue
		}
		if o != "" {
			s.exact[o] = struct{}{}
		}
	}
	return s
}

func (s *originSet) allows(origin string) bool {
	if s == nil {
		return false
	}
	if s.wildcard {
		return true
	}
	_, ok := s.exact[origin]
	return ok
}

func defaultIfEmpty(s []string, def ...string) []string {
	if len(s) == 0 {
		return def
	}
	return s
}
