package config

import (
	"reflect"
	"testing"
)

func TestParseBlockedUserAgents(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "unset", raw: "", want: nil},
		{name: "single entry", raw: "GPTBot", want: []string{"gptbot"}},
		{
			name: "multiple entries with whitespace",
			raw:  "GPTBot, Bytespider , AhrefsBot",
			want: []string{"gptbot", "bytespider", "ahrefsbot"},
		},
		{
			name: "leading and trailing commas drop empty entries",
			raw:  ",GPTBot,,Bytespider,",
			want: []string{"gptbot", "bytespider"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBlockedUserAgents(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseBlockedUserAgents(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}
