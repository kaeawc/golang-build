package mcpserver

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// requestStateTTL bounds how long a client may take between receiving an
// input request and retrying with the answer.
const requestStateTTL = 10 * time.Minute

// errRequestState is returned when a retried call carries a requestState
// that was not minted by this server for this exact operation.
var errRequestState = errors.New("invalid requestState")

// stateClaims is the payload of a multi round-trip requestState. SEP-2322
// requires servers to integrity-protect requestState because the client
// echoes it back verbatim; binding it to the tool and target means a state
// minted for "delete user 7" cannot be replayed to delete user 8.
type stateClaims struct {
	tokens.StandardClaims
	Tool   string `json:"tool"`
	Target string `json:"target"`
}

// stateSigner mints and verifies requestState tokens.
type stateSigner struct {
	tokens tokens.IssuerVerifier
	clk    clock.Clock
}

func newStateSigner(iv tokens.IssuerVerifier, clk clock.Clock) *stateSigner {
	if iv == nil {
		// A per-process random key is correct for stdio and single-instance
		// HTTP. Multi-instance deployments must share MCP_STATE_SECRET so a
		// retry can land on any replica.
		iv = tokens.NewHMAC256([]byte(rand.Text()), tokens.WithClock(clk))
	}
	return &stateSigner{tokens: iv, clk: clk}
}

func (s *stateSigner) issue(tool, target string) (string, error) {
	now := s.clk.Now()
	tok, err := s.tokens.Sign(stateClaims{
		StandardClaims: tokens.StandardClaims{
			IssuedAt:  now.Unix(),
			ExpiresAt: now.Add(requestStateTTL).Unix(),
		},
		Tool:   tool,
		Target: target,
	})
	if err != nil {
		return "", fmt.Errorf("sign requestState: %w", err)
	}
	return tok, nil
}

// open verifies state's signature and expiry, checks it was minted for
// tool, and returns the bound target.
func (s *stateSigner) open(state, tool string) (string, error) {
	if state == "" {
		return "", fmt.Errorf("%w: missing", errRequestState)
	}
	var c stateClaims
	if err := s.tokens.Verify(state, &c); err != nil {
		return "", fmt.Errorf("%w: %w", errRequestState, err)
	}
	if c.Tool != tool {
		return "", fmt.Errorf("%w: issued for tool %q", errRequestState, c.Tool)
	}
	return c.Target, nil
}

// verify is open plus a check that the state is bound to target.
func (s *stateSigner) verify(state, tool, target string) error {
	got, err := s.open(state, tool)
	if err != nil {
		return err
	}
	if got != target {
		return fmt.Errorf("%w: issued for target %q", errRequestState, got)
	}
	return nil
}
