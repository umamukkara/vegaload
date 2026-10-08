// Package sqlcommon holds the small pieces the SQL drivers share: reading
// the args option, and the min_rows and expect checks. Each driver keeps
// its own wire protocol.
package sqlcommon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ErrNoSQL is the error for a call that was given no SQL text.
func ErrNoSQL(driver string) error {
	return fmt.Errorf("%s: the SQL is required (pass it with -body)", driver)
}

// ParseArgs reads the args option, a JSON array. A whole number becomes an
// int64, so it is not rounded through a float. Any other number becomes a
// float64. A nested object or array becomes its JSON text, which a JSON
// column accepts. Errors start with the driver's name.
func ParseArgs(driver, raw string) ([]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var list []any
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf(`%s: args must be a JSON array, such as '[42, "abc"]'`, driver)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: args has text after the JSON array", driver)
	}
	for i, v := range list {
		switch x := v.(type) {
		case json.Number:
			if n, err := x.Int64(); err == nil {
				list[i] = n
			} else if f, err := x.Float64(); err == nil {
				list[i] = f
			} else {
				return nil, fmt.Errorf("%s: args[%d]: %q is not a number", driver, i, x)
			}
		case map[string]any, []any:
			b, err := json.Marshal(x)
			if err != nil {
				return nil, fmt.Errorf("%s: args[%d]: %w", driver, i, err)
			}
			list[i] = string(b)
		}
	}
	return list, nil
}

// Check applies min_rows (against rowCount, which counts every row) and
// expect (a substring of the text of any kept cell).
func Check(driver string, minRows, rowCount int, expect string, rows [][]any) error {
	if minRows > 0 && rowCount < minRows {
		return fmt.Errorf("%s: the result has %d rows, want at least %d", driver, rowCount, minRows)
	}
	if expect != "" {
		for _, row := range rows {
			for _, v := range row {
				if strings.Contains(fmt.Sprint(v), expect) {
					return nil
				}
			}
		}
		return fmt.Errorf("%s: no value in the result contains %q", driver, expect)
	}
	return nil
}
