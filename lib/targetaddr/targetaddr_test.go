package targetaddr

import "testing"

func TestValid(t *testing.T) {
	for _, test := range []struct {
		address string
		valid   bool
	}{
		{"10.0.0.1:5520", true},
		{"game.internal:5520", true},
		{"[2001:db8::1]:5520", true},
		{":5520", true},
		{"", false},
		{"game.internal", false},
		{"game.internal:0", false},
		{"game.internal:65536", false},
		{"[2001:db8::1:5520", false},
	} {
		if got := Valid(test.address); got != test.valid {
			t.Errorf("Valid(%q) = %t, want %t", test.address, got, test.valid)
		}
	}
}
