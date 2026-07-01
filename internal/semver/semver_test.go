package semver

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"v1.2.3", Version{1, 2, 3}, true},
		{"1.2.3", Version{1, 2, 3}, true},
		{" v0.0.1 ", Version{0, 0, 1}, true},
		{"v1.2.3-rc1", Version{1, 2, 3}, true},
		{"v1.2.3+build5", Version{1, 2, 3}, true},
		{"dev", Version{}, false},
		{"1.2", Version{}, false},
		{"v1.2.x", Version{}, false},
		{"", Version{}, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Parse(%q) = %+v,%v; se esperaba %+v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v2.0.0", "v1.9.9", false},
		{"v1.0.1", "v1.0.0", false},
		{"dev", "v1.0.0", false},    // no parseable → nunca "menor"
		{"v1.0.0", "basura", false}, // destino no parseable → false
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q,%q) = %v; se esperaba %v", c.a, c.b, got, c.want)
		}
	}
}
