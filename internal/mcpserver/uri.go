package mcpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Resource URIs and templates served by the server.
const (
	URIDirectory      = "users://directory"
	URIServerInfo     = "server://info"
	URILogo           = "assets://logo.png"
	URIReport         = "reports://latest"
	URITemplateUser   = "users://{id}"
	URITemplateAvatar = "users://{id}/avatar.png"
)

var errBadUserURI = errors.New("mcpserver: not a users://{id} URI")

// UserURI returns the canonical resource URI for a user.
func UserURI(id int64) string { return "users://" + strconv.FormatInt(id, 10) }

// AvatarURI returns the avatar resource URI for a user.
func AvatarURI(id int64) string { return UserURI(id) + "/avatar.png" }

// ParseUserURI extracts the user ID from a users://{id} or
// users://{id}/avatar.png URI. avatar reports which form matched.
//
// The SDK's RFC 6570 matcher decides which template handler runs, but it
// accepts any non-empty segment; this function is the strict validation that
// turns the matched string into a positive int64.
func ParseUserURI(uri string) (id int64, avatar bool, err error) {
	rest, ok := strings.CutPrefix(uri, "users://")
	if !ok {
		return 0, false, fmt.Errorf("%w: %q", errBadUserURI, uri)
	}
	rest, avatar = strings.CutSuffix(rest, "/avatar.png")
	if rest == "" || len(rest) > 19 {
		return 0, false, fmt.Errorf("%w: %q", errBadUserURI, uri)
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, false, fmt.Errorf("%w: %q", errBadUserURI, uri)
		}
	}
	id, err = strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rest {
		// Rejecting leading zeros keeps one canonical URI per user, so
		// subscriptions keyed by URI cannot be split across aliases.
		return 0, false, fmt.Errorf("%w: %q", errBadUserURI, uri)
	}
	return id, avatar, nil
}
