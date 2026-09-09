package adminpath

import "testing"

func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{"": "", "admin": "/admin", "/manage-72/": "/manage-72", "/private/control_9": "/private/control_9"} {
		got, err := Normalize(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"/", "//", "api", "API/key", "assets/admin", "member", "tickets/a", "a/../admin", "a//b", "a?b", "a#b", "a b", " a", "a%2fb", `a\b`, `a\"b`, "https://host/path"} {
		if _, err := Normalize(input); err == nil {
			t.Errorf("accepted unsafe/conflicting path %q", input)
		}
	}
}
