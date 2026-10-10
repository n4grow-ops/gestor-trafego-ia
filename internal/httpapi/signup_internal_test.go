package httpapi

import "testing"

func TestPhoneDisplay(t *testing.T) {
	for in, want := range map[string]string{
		"5549991911891":      "+55 49 99191-1891",
		"+55 (11) 3333-4444": "+55 11 3333-4444",
		"14155550100":        "+14155550100",
		"":                   "",
	} {
		if got := phoneDisplay(in); got != want {
			t.Errorf("phoneDisplay(%q) = %q, quero %q", in, got, want)
		}
	}
}
