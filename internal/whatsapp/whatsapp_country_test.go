package whatsapp

import "testing"

func TestWithCountry(t *testing.T) {
	for in, want := range map[string]string{
		"(11) 98888-7777":  "5511988887777",
		"1188887777":       "551188887777",
		"55 11 98888-7777": "5511988887777",
		"5549912345678":    "5549912345678",
		"":                 "",
	} {
		if got := WithCountry(in); got != want {
			t.Errorf("WithCountry(%q) = %q, want %q", in, got, want)
		}
	}
}
