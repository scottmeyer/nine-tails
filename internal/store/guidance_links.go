package store

import (
	"fmt"
	"sort"
	"strings"
)

// GuidanceLinkTarget parses the complete literal body of a guidance-link
// definition. It names an owner whose active guidance may be rendered; it is
// not an alias, record id, query, or a route to that owner's capsule.
func GuidanceLinkTarget(body string) (string, error) {
	agent := strings.TrimSpace(body)
	if err := ValidAgentName(agent); err != nil {
		return "", fmt.Errorf("%w: guidance link source must be an agent name: %v", ErrInvalid, err)
	}
	return agent, nil
}

// GuidanceLinkApplies evaluates the conjunction of a link, its source record,
// and the receiving context. A missing key is a wildcard, but every key named
// by two or more participants needs one value common to all of them. Pairwise
// conflict checks are insufficient for multivalued scopes (for example
// {a,b}, {b,c}, {a,c} has no common project).
func GuidanceLinkApplies(link, source, context Meta) bool {
	keys := map[string]bool{}
	for _, m := range []Meta{link, source, context} {
		for k := range m {
			keys[k] = true
		}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		var intersection map[string]bool
		for _, m := range []Meta{link, source, context} {
			values, ok := m[k]
			if !ok {
				continue
			}
			set := map[string]bool{}
			for _, value := range values {
				set[value] = true
			}
			if intersection == nil {
				intersection = set
				continue
			}
			for value := range intersection {
				if !set[value] {
					delete(intersection, value)
				}
			}
		}
		if intersection != nil && len(intersection) == 0 {
			return false
		}
	}
	return true
}
