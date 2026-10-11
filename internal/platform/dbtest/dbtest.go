// Package dbtest gives integration tests an isolated, fully migrated PostgreSQL database.
//
// TEST_DATABASE_URL must point at a server where the user may CREATE DATABASE. A template
// database is migrated once per schema version; each New call clones it (fast) and drops
// the clone when the test ends.
package dbtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/database"
	"desakita/migrations"
)

// templateLockKey serializes template creation across concurrently running test binaries.
const templateLockKey = 727274001

// BaseURL returns TEST_DATABASE_URL or fails the test.
func BaseURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Fatal("TEST_DATABASE_URL is not set; integration tests need PostgreSQL (see CLAUDE.md)")
	}
	return u
}

// New returns a pool connected to a fresh, migrated database.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := NewURL(t, true)
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewURL creates a fresh database and returns its URL. If migrated is false the database
// is empty, which lets migration tests run from scratch.
func NewURL(t testing.TB, migrated bool) string {
	t.Helper()
	ctx := context.Background()
	base := BaseURL(t)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)

	name := "desakita_t_" + randomHex(t, 6)
	stmt := fmt.Sprintf("CREATE DATABASE %s", name)
	if migrated {
		// Hold the lock while cloning too: CREATE DATABASE ... TEMPLATE fails if the
		// template is being built or cloned by another test binary at the same moment.
		if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
			t.Fatalf("lock template: %v", err)
		}
		defer admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", templateLockKey) //nolint:errcheck
		stmt += fmt.Sprintf(" TEMPLATE %s", ensureTemplate(t, admin, base))
	}
	if _, err := admin.Exec(ctx, stmt); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	return withDatabase(t, base, name)
}

// ensureTemplate returns the migrated template's name, building it if needed.
// The caller must hold templateLockKey.
func ensureTemplate(t testing.TB, admin *pgx.Conn, base string) string {
	t.Helper()
	ctx := context.Background()
	name := "desakita_tmpl_" + schemaHash(t)

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatalf("check template: %v", err)
	}
	if exists {
		return name
	}

	// Build under a temporary name and rename, so a crash never leaves a half-migrated template.
	building := name + "_building"
	admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", building)) //nolint:errcheck
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", building)); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := database.MigrateUp(withDatabase(t, base, building)); err != nil {
		t.Fatalf("migrate template: %v", err)
	}
	// RENAME fails while the migrator's connections are still closing; retry briefly.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := admin.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", building, name))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rename template: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return name
}

// schemaHash fingerprints the migration files so schema changes produce a new template.
func schemaHash(t testing.TB) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func withDatabase(t testing.TB, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
