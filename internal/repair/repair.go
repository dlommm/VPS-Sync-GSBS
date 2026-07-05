package repair

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsbs/gsbs/server/store"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
)

// DB reconciles a sanitized publisher database with the GSBS schema.
//
// The publisher's gsbs.db is typically seeded from production with the user
// tables (users, clients, save_versions, …) dropped so no user data lives on
// the VPS — but the schema version stamp survives the strip, so any newer GSBS
// migration that alters one of the dropped tables fails with "no such table".
//
// The fix is generic rather than a hardcoded table list: build a pristine,
// fully-migrated GSBS database in a temp dir, create every table/index/trigger
// it has that the target lacks (empty, current shape), and stamp the target
// with the pristine schema version so the already-covered migrations don't
// re-run against the freshly created objects. PCGW data is never touched.
//
// Returns the names of created objects; empty means the schema was already
// complete and nothing was changed (including the version stamp).
func DB(dbPath string) ([]string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found at %s", dbPath)
	}

	pristinePath, pristineVersion, err := buildPristine()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Dir(pristinePath))

	pristine, err := sql.Open("sqlite3", pristinePath)
	if err != nil {
		return nil, err
	}
	defer pristine.Close()

	target, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer target.Close()

	var targetVersion int
	if err := target.QueryRow("PRAGMA user_version").Scan(&targetVersion); err != nil {
		return nil, fmt.Errorf("read target schema version: %w", err)
	}

	existing := map[string]bool{}
	rows, err := target.Query(`SELECT name FROM sqlite_master`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		existing[name] = true
	}
	rows.Close()

	// Tables first so indexes/triggers on freshly created tables succeed.
	wanted, err := pristine.Query(`
		SELECT name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name`)
	if err != nil {
		return nil, err
	}
	type object struct{ name, ddl string }
	var missing []object
	for wanted.Next() {
		var o object
		if err := wanted.Scan(&o.name, &o.ddl); err != nil {
			wanted.Close()
			return nil, err
		}
		if !existing[o.name] {
			missing = append(missing, o)
		}
	}
	wanted.Close()

	if len(missing) == 0 {
		log.Info().Int("schema_version", targetVersion).Msg("repair-db: schema already complete, nothing to do")
		return nil, nil
	}

	tx, err := target.Begin()
	if err != nil {
		return nil, err
	}
	var created []string
	for _, o := range missing {
		if _, err := tx.Exec(o.ddl); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("create %s: %w", o.name, err)
		}
		created = append(created, o.name)
	}
	if pristineVersion > targetVersion {
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", pristineVersion)); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("stamp schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	log.Info().
		Strs("created", created).
		Int("from_version", targetVersion).
		Int("to_version", pristineVersion).
		Msg("repair-db: recreated missing schema objects")
	return created, nil
}

// buildPristine creates a fully-migrated empty GSBS database and returns its
// path and schema version. The caller removes the containing temp dir.
func buildPristine() (string, int, error) {
	dir, err := os.MkdirTemp("", "gsbs-pristine-*")
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, "pristine.db")
	st, err := store.NewSQLite(path)
	if err != nil {
		os.RemoveAll(dir)
		return "", 0, fmt.Errorf("build pristine schema: %w", err)
	}
	st.Close()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		os.RemoveAll(dir)
		return "", 0, err
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		os.RemoveAll(dir)
		return "", 0, err
	}
	return path, version, nil
}

// IsSchemaError reports whether err looks like the missing-table migration
// failure a sanitized publisher DB produces, so callers can point the operator
// at repair-db.
func IsSchemaError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "migration step") && strings.Contains(msg, "no such table")
}
