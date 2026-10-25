package dbcore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var (
	ErrSchemaVersion = errors.New("schema version mismatch")
	ErrLockTimeout   = errors.New("schema lock timeout")
)

func ValidateIdentifier(value string) error {
	if len(value) > 63 || !identifier.MatchString(value) {
		return fmt.Errorf("invalid PostgreSQL identifier: %q", value)
	}
	return nil
}

func ValidateSchemaName(value string) error {
	if err := ValidateIdentifier(value); err != nil {
		return err
	}
	if value == "information_schema" || strings.HasPrefix(value, "pg_") {
		return fmt.Errorf("reserved schema name: %s", value)
	}
	return nil
}

func QuoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

func (c Config) DSN() (string, error) {
	if c.Host == "" || c.User == "" || c.Database == "" || c.Port < 1 || c.Port > 65535 {
		return "", errors.New("incomplete PostgreSQL connection configuration")
	}
	mode := c.SSLMode
	if mode == "" {
		mode = "disable"
	}
	switch mode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return "", fmt.Errorf("invalid sslmode: %s", mode)
	}
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(c.User, c.Password), Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: "/" + c.Database}
	q := u.Query()
	q.Set("sslmode", mode)
	q.Set("connect_timeout", "5")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Open は再接続時にも指定 role を設定する。接続主体 session_user は変えない。
func (c Config) Open(ctx context.Context, role string) (*sql.DB, error) {
	dsn, err := c.DSN()
	if err != nil {
		return nil, err
	}
	if role != "" {
		if err = ValidateIdentifier(role); err != nil {
			return nil, err
		}
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.RuntimeParams["statement_timeout"] = "30000"
	db := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		if role == "" {
			return nil
		}
		_, err := conn.Exec(ctx, "SET ROLE "+QuoteIdentifier(role))
		return err
	}))
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
