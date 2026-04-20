package internal

// dialect.go — PostgreSQL / SQLite abstraction layer.
//
// When DATABASE_URL is set, InitPG() is called and pgMode becomes true.
// All SQL execution helpers (execDB / queryDB / queryRowDB / prepareDB /
// prepareTx) transparently rewrite "?" placeholders to "$1", "$2", …
// so that the rest of the codebase can use standard "?" style everywhere.

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
)

// pgMode is true when PostgreSQL is active.
var pgMode bool

// pgBaseURL is the raw DATABASE_URL value (without user-schema suffix).
var pgBaseURL string

// IsPGMode returns true when the backing store is PostgreSQL.
func IsPGMode() bool { return pgMode }

// InitPG validates DATABASE_URL and enables PostgreSQL mode.
// Must be called before InitAuthDB / GetUserDB.
func InitPG(dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("postgres: open: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("postgres: ping: %w", err)
	}
	pgMode = true
	pgBaseURL = dsn
	return nil
}

// openPGConn opens a PostgreSQL connection with the given schema as the
// default search_path.
func openPGConn(schema string) (*sql.DB, error) {
	sep := "?"
	if strings.Contains(pgBaseURL, "?") {
		sep = "&"
	}
	dsn := pgBaseURL + sep + "search_path=" + schema
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// pgUserSchema returns the PG schema name for a userID.
func pgUserSchema(userID string) string {
	return "u_" + SanitizeUsername(userID)
}

// rq rewrites SQL "?" placeholders to "$1", "$2", … for PostgreSQL.
// Returns the query unchanged in SQLite mode.
func rq(query string) string {
	if !pgMode {
		return query
	}
	var b strings.Builder
	n := 0
	inStr := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' {
			inStr = !inStr
		}
		if c == '?' && !inStr {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ─── Helper wrappers ──────────────────────────────────────────────────────────

// execDB calls db.Exec with rq() applied to the query.
func execDB(db *sql.DB, query string, args ...interface{}) (sql.Result, error) {
	return db.Exec(rq(query), args...)
}

// queryDB calls db.Query with rq() applied to the query.
func queryDB(db *sql.DB, query string, args ...interface{}) (*sql.Rows, error) {
	return db.Query(rq(query), args...)
}

// queryRowDB calls db.QueryRow with rq() applied to the query.
func queryRowDB(db *sql.DB, query string, args ...interface{}) *sql.Row {
	return db.QueryRow(rq(query), args...)
}

// prepareDB calls db.Prepare with rq() applied to the query.
func prepareDB(db *sql.DB, query string) (*sql.Stmt, error) {
	return db.Prepare(rq(query))
}

// prepareTx calls tx.Prepare with rq() applied to the query.
func prepareTx(tx *sql.Tx, query string) (*sql.Stmt, error) {
	return tx.Prepare(rq(query))
}

// ─── PostgreSQL DDL ───────────────────────────────────────────────────────────

// pgMessagesDDL creates the messages table + indexes in a PostgreSQL schema.
// body_tsv is a generated column for full-text search (PG 12+).
const pgMessagesDDL = `
CREATE TABLE IF NOT EXISTS messages (
	id               BIGSERIAL PRIMARY KEY,
	record_type      INTEGER   NOT NULL DEFAULT 1,
	address          TEXT      NOT NULL,
	body             TEXT,
	type             INTEGER   NOT NULL,
	date             BIGINT    NOT NULL,
	read             INTEGER   DEFAULT 0,
	thread_id        BIGINT,
	subject          TEXT,
	media_type       TEXT,
	media_data       BYTEA,
	protocol         INTEGER,
	status           INTEGER,
	service_center   TEXT,
	sub_id           INTEGER,
	contact_name     TEXT,
	sender           TEXT,
	content_type     TEXT,
	read_report      INTEGER,
	read_status      INTEGER,
	message_id       TEXT,
	message_size     INTEGER,
	message_type     INTEGER,
	sim_slot         INTEGER,
	addresses        TEXT,
	duration         INTEGER,
	presentation     INTEGER,
	subscription_id  TEXT,
	body_tsv         TSVECTOR GENERATED ALWAYS AS (
	                     to_tsvector('english',
	                         COALESCE(body, '') || ' ' || COALESCE(contact_name, ''))
	                 ) STORED
);

CREATE INDEX IF NOT EXISTS idx_address         ON messages(address);
CREATE INDEX IF NOT EXISTS idx_date            ON messages(date);
CREATE INDEX IF NOT EXISTS idx_thread          ON messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_record_type     ON messages(record_type);
CREATE INDEX IF NOT EXISTS idx_record_type_date ON messages(record_type, date);
CREATE INDEX IF NOT EXISTS idx_body_tsv        ON messages USING GIN(body_tsv);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_unique ON messages(
	record_type, address, date, type,
	COALESCE(body, ''), COALESCE(content_type, ''),
	COALESCE(message_id, ''), COALESCE(duration, 0)
);
`

// pgAuthDDL creates the auth + settings + gdrive_tokens tables in PostgreSQL.
const pgAuthDDL = `
CREATE TABLE IF NOT EXISTS users (
	id            TEXT   PRIMARY KEY,
	username      TEXT   NOT NULL UNIQUE,
	password_hash TEXT   NOT NULL,
	created_at    BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id          TEXT   PRIMARY KEY,
	user_id     TEXT   NOT NULL,
	created_at  BIGINT NOT NULL,
	expires_at  BIGINT NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS settings (
	user_id       TEXT  PRIMARY KEY,
	settings_json TEXT  NOT NULL DEFAULT '{}',
	updated_at    BIGINT NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id   ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS gdrive_tokens (
	user_id       TEXT   PRIMARY KEY,
	access_token  TEXT   NOT NULL,
	refresh_token TEXT   NOT NULL DEFAULT '',
	token_type    TEXT   NOT NULL DEFAULT 'Bearer',
	expiry        BIGINT NOT NULL DEFAULT 0,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
`
