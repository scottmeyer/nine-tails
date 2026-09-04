package tokens

import "testing"

func TestEstimate(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abc", 1},
		{"abcd", 2},    // 4/3.5 = 1.14 → 2
		{"abcdefg", 2}, // 7/3.5 = 2
		{"abcdefgh", 3},
	}
	for _, c := range cases {
		if got := Estimate(c.in); got != c.want {
			t.Errorf("Estimate(%q) = %d, want %d", c.in, got, c.want)
		}
		if got := EstimateBytes([]byte(c.in)); got != c.want {
			t.Errorf("EstimateBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
