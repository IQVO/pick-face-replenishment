package shared_test

import (
	"strings"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

func TestIsBlank(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"space", " ", true},
		{"tab", "\t", true},
		{"newline", "\n", true},
		{"vertical tab", "\v", true},
		{"form feed", "\f", true},
		{"carriage return", "\r", true},
		{"no-break space", "\u00a0", true},
		{"byte order mark", "\ufeff", true},
		{"line separator", "\u2028", true},
		{"paragraph separator", "\u2029", true},
		{"ogham space mark", "\u1680", true},
		{"en quad", "\u2000", true},
		{"hair space", "\u200a", true},
		{"narrow no-break space", "\u202f", true},
		{"medium mathematical space", "\u205f", true},
		{"ideographic space", "\u3000", true},
		{"mixed spaces", " \t\u3000\ufeff ", true},
		{"letter", "a", false},
		{"letter between spaces", " a ", false},
		{"digit", "0", false},
		// ECMA-262 does not treat NEL as white space; strings.TrimSpace does.
		{"next line is not blank", "\u0085", false},
		// Zero width space is a format character, not white space.
		{"zero width space is not blank", "\u200b", false},
		{"unit separator is not blank", "\x1f", false},
		{"nul is not blank", "\x00", false},
		{"slash is not blank", "/", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shared.IsBlank(tc.in); got != tc.want {
				t.Fatalf("IsBlank(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// strings.TrimSpace is the wrong tool: pin the two runes where it disagrees.
func TestIsBlankDisagreesWithTrimSpaceWhereECMADoes(t *testing.T) {
	if strings.TrimSpace("\u0085") != "" {
		t.Fatal("test premise: TrimSpace treats NEL as white space")
	}
	if shared.IsBlank("\u0085") {
		t.Fatal("NEL must not be blank under ECMA-262")
	}
	if strings.TrimSpace("\ufeff") == "" {
		t.Fatal("test premise: TrimSpace does not treat the BOM as white space")
	}
	if !shared.IsBlank("\ufeff") {
		t.Fatal("the BOM must be blank under ECMA-262")
	}
}

func TestIsSpace(t *testing.T) {
	if !shared.IsSpace(' ') || shared.IsSpace('x') {
		t.Fatal("space is a space, x is not")
	}
}
