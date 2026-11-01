// Package db wraps database/sql, keeping placeholders and transactions portable.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	Driver string
}
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Tx struct {
	*sql.Tx
	Driver string
}

func Open(ctx context.Context, driver, dsn string) (*DB, error) {
	name := driver
	switch driver {
	case "sqlite":
		if !strings.HasPrefix(dsn, "file:") && dsn != ":memory:" {
			abs, err := filepath.Abs(dsn)
			if err != nil {
				return nil, err
			}
			if err = os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return nil, err
			}
			dsn = (&url.URL{Scheme: "file", Path: abs}).String()
		}
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	case "postgres":
		name = "pgx"
	default:
		return nil, fmt.Errorf("unsupported driver %q", driver)
	}
	raw, err := sql.Open(name, dsn)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		raw.SetMaxOpenConns(1)
		raw.SetMaxIdleConns(1)
	}
	if err = raw.PingContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return &DB{raw, driver}, nil
}
func (d *DB) Rebind(q string) string { return Rebind(d.Driver, q) }
func (d *DB) ExecContext(c context.Context, q string, a ...any) (sql.Result, error) {
	return d.DB.ExecContext(c, d.Rebind(q), a...)
}
func (d *DB) QueryContext(c context.Context, q string, a ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(c, d.Rebind(q), a...)
}
func (d *DB) QueryRowContext(c context.Context, q string, a ...any) *sql.Row {
	return d.DB.QueryRowContext(c, d.Rebind(q), a...)
}
func (t *Tx) ExecContext(c context.Context, q string, a ...any) (sql.Result, error) {
	return t.Tx.ExecContext(c, Rebind(t.Driver, q), a...)
}
func (t *Tx) QueryContext(c context.Context, q string, a ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(c, Rebind(t.Driver, q), a...)
}
func (t *Tx) QueryRowContext(c context.Context, q string, a ...any) *sql.Row {
	return t.Tx.QueryRowContext(c, Rebind(t.Driver, q), a...)
}
func (d *DB) WithTx(c context.Context, fn func(*Tx) error) error {
	t, err := d.DB.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer t.Rollback()
	if err = fn(&Tx{t, d.Driver}); err != nil {
		return err
	}
	return t.Commit()
}

// Rebind replaces placeholders outside SQL strings, identifiers, comments and
// PostgreSQL dollar-quoted strings. All application SQL uses '?' placeholders.
func Rebind(driver, q string) string {
	if driver != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); {
		start := i
		switch {
		case q[i] == '\'' || q[i] == '"' || q[i] == '`':
			quote := q[i]
			i++
			for i < len(q) {
				if q[i] == quote {
					i++
					if i < len(q) && q[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
		case strings.HasPrefix(q[i:], "--"):
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case strings.HasPrefix(q[i:], "/*"):
			i += 2
			depth := 1
			for i < len(q) && depth > 0 {
				if strings.HasPrefix(q[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(q[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
		case q[i] == '$':
			j := i + 1
			for j < len(q) && ((q[j] >= 'a' && q[j] <= 'z') || (q[j] >= 'A' && q[j] <= 'Z') || (q[j] >= '0' && q[j] <= '9') || q[j] == '_') {
				j++
			}
			if j < len(q) && q[j] == '$' {
				tag := q[i : j+1]
				if end := strings.Index(q[j+1:], tag); end >= 0 {
					i = j + 1 + end + len(tag)
				} else {
					i++
				}
			} else {
				i++
			}
		case q[i] == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			i++
			continue
		default:
			i++
		}
		b.WriteString(q[start:i])
	}
	return b.String()
}
