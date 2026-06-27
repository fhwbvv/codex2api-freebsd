package admin

import "testing"

func TestCollectInviteEmails(t *testing.T) {
	t.Run("dedup and trim from list + text", func(t *testing.T) {
		got, err := collectInviteEmails(
			[]string{"A@example.com", " b@example.com "},
			"a@example.com\nc@example.com, d@example.com",
			10,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// A@ 与 a@ 视为重复（忽略大小写），保留首次出现的大小写形式。
		want := []string{"A@example.com", "b@example.com", "c@example.com", "d@example.com"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d]=%q, want %q (full: %v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("rejects invalid email", func(t *testing.T) {
		if _, err := collectInviteEmails([]string{"not-an-email"}, "", 10); err == nil {
			t.Fatal("expected error for invalid email")
		}
	})

	t.Run("empty input errors", func(t *testing.T) {
		if _, err := collectInviteEmails(nil, "  ", 10); err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("exceeds cap", func(t *testing.T) {
		if _, err := collectInviteEmails([]string{"a@x.com", "b@x.com", "c@x.com"}, "", 2); err == nil {
			t.Fatal("expected error when exceeding cap")
		}
	})
}
