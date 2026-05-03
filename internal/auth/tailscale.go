package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// TailscaleResolver looks up the Tailscale identity for a request's
// remote address. It mirrors the shape of (*tailscale.LocalClient).WhoIs
// without forcing this package to depend on tailscale.com/...
//
// Wire it in the composition root, e.g.:
//
//	ts := &tsnet.Server{Hostname: "internal-tool"}
//	lc, _ := ts.LocalClient()
//	resolver := func(ctx context.Context, addr string) (auth.Identity, error) {
//	    res, err := lc.WhoIs(ctx, addr)
//	    if err != nil { return auth.Identity{}, err }
//	    return auth.Identity{
//	        Subject:  res.UserProfile.LoginName,
//	        Email:    res.UserProfile.LoginName,
//	        Provider: "tailscale",
//	        Claims:   map[string]any{"node": res.Node.ComputedName},
//	    }, nil
//	}
//	authn := auth.Tailscale(resolver)
//
// The resolver should return ErrNoIdentity for traffic that isn't on the
// tailnet (e.g. a localhost dev probe), so Require returns 401 instead
// of a 500.
type TailscaleResolver func(ctx context.Context, remoteAddr string) (Identity, error)

// Tailscale returns an Authenticator that resolves identity from the
// request's RemoteAddr using resolve. resolve must not be nil.
func Tailscale(resolve TailscaleResolver) Authenticator {
	if resolve == nil {
		panic("auth: TailscaleResolver must not be nil")
	}
	return tailscaleAuth{resolve: resolve}
}

type tailscaleAuth struct {
	resolve TailscaleResolver
}

func (t tailscaleAuth) Identify(r *http.Request) (Identity, error) {
	if r.RemoteAddr == "" {
		return Identity{}, ErrNoIdentity
	}
	id, err := t.resolve(r.Context(), r.RemoteAddr)
	if err != nil {
		if errors.Is(err, ErrNoIdentity) || errors.Is(err, ErrInvalidToken) {
			return Identity{}, err
		}
		return Identity{}, fmt.Errorf("auth: tailscale resolve: %w", err)
	}
	if id.Provider == "" {
		id.Provider = "tailscale"
	}
	return id, nil
}
