package store

import (
	"fmt"
	"strings"
)

// StateLinkTarget parses the complete literal body of a state-link definition.
// It names live working state, never a record id, file, query, or another link.
func StateLinkTarget(body string) (agent, name string, err error) {
	agent, name, ok := strings.Cut(strings.TrimSpace(body), "/")
	if !ok || agent == "" || name == "" {
		return "", "", fmt.Errorf("%w: state link target must be <agent>/<state-name>", ErrInvalid)
	}
	if err := ValidAgentName(agent); err != nil {
		return "", "", err
	}
	if err := ValidRecordName("state", name); err != nil {
		return "", "", err
	}
	return agent, name, nil
}
