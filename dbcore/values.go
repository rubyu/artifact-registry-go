package dbcore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func SHA256DigestValue(payload []byte) string {
	value := sha256.Sum256(payload)
	return hex.EncodeToString(value[:])
}
func ValidateDigestValue(value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("invalid SHA-256 digest value")
	}
	return nil
}
func VerifyDigestValue(payload []byte, value string) bool {
	return ValidateDigestValue(value) == nil && SHA256DigestValue(payload) == value
}

type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func AllocateUUID7Many(ctx context.Context, conn Queryer, count int) ([]string, error) {
	if count < 0 {
		return nil, fmt.Errorf("count must be nonnegative")
	}
	result := make([]string, 0, count)
	if count == 0 {
		return result, nil
	}
	rows, err := conn.QueryContext(ctx, "SELECT uuidv7()::text FROM generate_series(1,$1)", count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
