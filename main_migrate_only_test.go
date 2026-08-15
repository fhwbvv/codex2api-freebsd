package main

import "testing"

func TestMigrateOnlyEnabled(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "1", want: true},
		{value: "true", want: true},
		{value: " TRUE ", want: true},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "", want: false},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("CODEX_MIGRATE_ONLY", test.value)
			if got := migrateOnlyEnabled(); got != test.want {
				t.Fatalf("migrateOnlyEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}
