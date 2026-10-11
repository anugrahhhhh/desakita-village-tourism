// Package session configures cookie sessions stored in PostgreSQL.
package session

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keys shared across modules.
const (
	KeyUserID = "user_id"
	KeyFlash  = "flash"
)

// New returns a session manager. cleanup is how often expired rows are purged (0 disables it).
func New(pool *pgxpool.Pool, secure bool, cleanup time.Duration) *scs.SessionManager {
	sm := scs.New()
	sm.Store = pgxstore.NewWithCleanupInterval(pool, cleanup)
	sm.Lifetime = 30 * 24 * time.Hour
	sm.IdleTimeout = 7 * 24 * time.Hour
	sm.Cookie.Name = "desakita_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secure
	sm.Cookie.Persist = true
	return sm
}
