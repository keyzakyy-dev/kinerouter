package gateway

import "testing"

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1}, {"0.1.0", "0.1.0", 0}, {"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1}, {"v", "0.0.0", 0}, {"0.1.10", "0.1.9", 1},
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Fatalf("compareSemver(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
