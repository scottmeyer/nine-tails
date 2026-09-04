// Package tokens provides the deterministic, conservative token estimate used
// for capsule budgeting. No tokenizer is consulted; the estimate is
// ceil(bytes / 3.5), which overestimates for ordinary English prose and code.
package tokens

// Estimate returns the estimated token count for s.
func Estimate(s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	// ceil(n / 3.5) == ceil(2n / 7)
	return (2*n + 6) / 7
}

// EstimateBytes is Estimate for a byte slice.
func EstimateBytes(b []byte) int {
	n := len(b)
	if n == 0 {
		return 0
	}
	return (2*n + 6) / 7
}
