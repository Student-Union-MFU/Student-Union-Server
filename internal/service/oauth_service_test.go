package service

import "testing"

// The tokeninfo endpoint renders booleans as strings, so a plain `== "true"`
// against the raw JSON would reject the very tokens this check exists to
// accept — and a bare `bool` field would silently decode to false instead of
// erroring, turning a verified address into a rejected one.
func TestIsTrue(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"tokeninfo string form", `"true"`, true},
		{"native bool form", `true`, true},
		{"string false", `"false"`, false},
		{"native false", `false`, false},
		{"absent claim decodes to nothing", ``, false},
		{"null", `null`, false},
		{"unexpected value", `"yes"`, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isTrue([]byte(c.raw)); got != c.want {
				t.Errorf("isTrue(%s) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}
