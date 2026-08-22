package database

import (
	"database/sql"
	"testing"
)

func TestAccountRowGetCredentialInt64SliceNormalizesValues(t *testing.T) {
	row := &AccountRow{
		Credentials: map[string]interface{}{
			"allowed_api_key_ids": []interface{}{float64(3), float64(1), float64(3), float64(0)},
		},
	}

	got := row.GetCredentialInt64Slice("allowed_api_key_ids")
	want := []int64{1, 3}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestAccountRowGetCredentialInt64SliceMissingFieldReturnsEmptySlice(t *testing.T) {
	row := &AccountRow{Credentials: map[string]interface{}{}}
	got := row.GetCredentialInt64Slice("allowed_api_key_ids")
	if len(got) != 0 {
		t.Fatalf("got = %v, want empty slice", got)
	}
}

func TestNormalizeCodexCLIVersionSyncIntervalHours(t *testing.T) {
	cases := map[int]int{
		0:    12,
		-5:   12,
		1:    1,
		12:   12,
		720:  720,
		721:  720,
		9999: 720,
	}
	for in, want := range cases {
		if got := NormalizeCodexCLIVersionSyncIntervalHours(in); got != want {
			t.Errorf("NormalizeCodexCLIVersionSyncIntervalHours(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSQLOpenDriverNameMapsPostgresToPgx(t *testing.T) {
	if got := sqlOpenDriverName("postgres"); got != "pgx" {
		t.Fatalf("sqlOpenDriverName(postgres) = %q, want pgx", got)
	}
	if got := sqlOpenDriverName("sqlite"); got != "sqlite" {
		t.Fatalf("sqlOpenDriverName(sqlite) = %q, want sqlite", got)
	}
}

func TestQuotePostgresIdentEscapesAndTruncates(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"public", `"public"`},
		{`weird"name`, `"weird""name"`},
		{"ok\x00DROP", `"ok"`},
	}
	for _, tc := range cases {
		if got := quotePostgresIdent(tc.in); got != tc.want {
			t.Fatalf("quotePostgresIdent(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestPostgresInt8ArrayTextLiteral(t *testing.T) {
	value, err := postgresInt8Array{3, 1, 2}.Value()
	if err != nil {
		t.Fatal(err)
	}
	if value != "{3,1,2}" {
		t.Fatalf("Value() = %#v, want {3,1,2}", value)
	}
	empty, err := postgresInt8Array{}.Value()
	if err != nil || empty != "{}" {
		t.Fatalf("empty Value() = %#v err=%v, want {}", empty, err)
	}
	nilValue, err := postgresInt8Array(nil).Value()
	if err != nil || nilValue != nil {
		t.Fatalf("nil Value() = %#v err=%v, want nil", nilValue, err)
	}
}

func TestPgxStdlibDriverIsRegistered(t *testing.T) {
	db, err := sql.Open("pgx", "host=127.0.0.1 port=1 user=x password=x dbname=x sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open(pgx) failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
