package auth

import "net/http"

// Static returns an Authenticator that yields the same Identity for
// every request. Intended for local development and tests; never wire
// this into a public-facing build.
func Static(id Identity) Authenticator {
	if id.Provider == "" {
		id.Provider = "static"
	}
	return staticAuth{id: id}
}

type staticAuth struct{ id Identity }

func (s staticAuth) Identify(*http.Request) (Identity, error) { return s.id, nil }
