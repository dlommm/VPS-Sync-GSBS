package repair

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gsbs/gsbs/server/store"
)

// Reproduces the sanitized-seed scenario: a full GSBS DB has its user tables
// dropped but keeps the schema version stamp. Opening it with the GSBS store
// must fail before repair and succeed after.
func TestRepairSanitizedDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gsbs.db")
	st, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"save_versions", "clients", "sessions", "users"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl); err != nil {
			t.Fatalf("drop %s: %v", tbl, err)
		}
	}
	// Rewind the stamp so pending migrations re-run against the stripped schema,
	// like a seed taken from an older production server.
	if _, err := db.Exec("PRAGMA user_version = 19"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := store.NewSQLite(dbPath); err == nil {
		t.Fatal("expected migration failure on sanitized DB")
	} else if !IsSchemaError(err) {
		t.Fatalf("IsSchemaError should recognize %v", err)
	}

	created, err := DB(dbPath)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(created) == 0 {
		t.Fatal("repair should have recreated dropped tables")
	}

	st2, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("GSBS store still cannot open repaired DB: %v", err)
	}
	st2.Close()

	// Second run is a no-op.
	created, err = DB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("second repair should be a no-op, created %v", created)
	}
}

func TestIsSchemaError(t *testing.T) {
	if IsSchemaError(nil) {
		t.Fatal("nil is not a schema error")
	}
	if IsSchemaError(fmt.Errorf("connection refused")) {
		t.Fatal("unrelated error misclassified")
	}
	if !IsSchemaError(fmt.Errorf("migration step 21: no such table: save_versions")) {
		t.Fatal("real schema error not recognized")
	}
}
