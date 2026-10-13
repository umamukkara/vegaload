package inputs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A placeholder is {data.NAME.COLUMN} or the same with a :int or :float
// suffix. NAME and COLUMN match a script identifier. Anything else that
// starts with {data. is left as it was written.
const placeholderPrefix = "{data."

// Slot is one placeholder in a template.
type Slot struct {
	Set    string
	Column string
	suffix string // "", "int", or "float"
	asJSON bool   // an args value with no suffix is written as JSON
}

// Template is a string split once into literal parts and slots.
// Filling a template that has no slots returns the original string.
type Template struct {
	raw   string
	parts []string
	slots []Slot
}

// Parse splits s into literals and placeholders. asArgs is true for an
// -opt args value: a placeholder with no suffix is replaced by the JSON
// form of the cell. Everywhere else the cell is inserted as plain text.
func Parse(s string, asArgs bool) (Template, error) {
	var parts []string
	var slots []Slot
	var b strings.Builder
	i := 0
	for i < len(s) {
		j := strings.Index(s[i:], placeholderPrefix)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		j += i
		b.WriteString(s[i:j])
		slot, end, ok := scanSlot(s, j)
		if !ok {
			b.WriteString(placeholderPrefix)
			i = j + len(placeholderPrefix)
			continue
		}
		parts = append(parts, b.String())
		b.Reset()
		slot.asJSON = asArgs && slot.suffix == ""
		slots = append(slots, slot)
		i = end
	}
	if len(slots) == 0 {
		return Template{raw: s}, nil
	}
	parts = append(parts, b.String())
	return Template{parts: parts, slots: slots}, nil
}

// Slots lists the placeholders, in order.
func (t Template) Slots() []Slot {
	out := make([]Slot, len(t.slots))
	copy(out, t.slots)
	return out
}

// HasSlots reports whether Fill will read a row.
func (t Template) HasSlots() bool { return len(t.slots) > 0 }

func scanSlot(s string, i int) (Slot, int, bool) {
	// i points at '{'.
	rest := s[i+len(placeholderPrefix):]
	name, rest, ok := ident(rest)
	if !ok || !strings.HasPrefix(rest, ".") {
		return Slot{}, 0, false
	}
	rest = rest[1:]
	column, rest, ok := ident(rest)
	if !ok {
		return Slot{}, 0, false
	}
	var suffix string
	if strings.HasPrefix(rest, ":") {
		switch {
		case strings.HasPrefix(rest, ":int}"):
			suffix = "int"
			rest = rest[len(":int"):]
		case strings.HasPrefix(rest, ":float}"):
			suffix = "float"
			rest = rest[len(":float"):]
		default:
			return Slot{}, 0, false
		}
	}
	if !strings.HasPrefix(rest, "}") {
		return Slot{}, 0, false
	}
	end := len(s) - len(rest) + 1
	return Slot{Set: name, Column: column, suffix: suffix}, end, true
}

func ident(s string) (string, string, bool) {
	if s == "" || !isIdentStart(s[0]) {
		return "", s, false
	}
	i := 1
	for i < len(s) && isIdentCont(s[i]) {
		i++
	}
	return s[:i], s[i:], true
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// Fill replaces each placeholder from rows, which maps a data set name
// to the one row this call drew. A template with no slots returns the
// original string and does not allocate.
func (t Template) Fill(rows map[string]map[string]any) (string, error) {
	if len(t.slots) == 0 {
		return t.raw, nil
	}
	var b strings.Builder
	for i, slot := range t.slots {
		b.WriteString(t.parts[i])
		row := rows[slot.Set]
		if row == nil {
			return "", fmt.Errorf("data set %s is not loaded", slot.Set)
		}
		val, ok := row[slot.Column]
		if !ok {
			return "", fmt.Errorf("data set %s: column %s does not exist", slot.Set, slot.Column)
		}
		text, err := formatCell(slot, val)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	b.WriteString(t.parts[len(t.slots)])
	return b.String(), nil
}

func formatCell(slot Slot, val any) (string, error) {
	switch slot.suffix {
	case "int":
		n, err := asInt(val)
		if err != nil {
			return "", fmt.Errorf("data set %s: column %s is not an integer", slot.Set, slot.Column)
		}
		return strconv.FormatInt(n, 10), nil
	case "float":
		n, err := asFloat(val)
		if err != nil {
			return "", fmt.Errorf("data set %s: column %s is not a number", slot.Set, slot.Column)
		}
		return strconv.FormatFloat(n, 'g', -1, 64), nil
	}
	if slot.asJSON {
		raw, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("data set %s: column %s cannot be written as JSON", slot.Set, slot.Column)
		}
		return string(raw), nil
	}
	return plainText(val), nil
}

func asInt(val any) (int64, error) {
	switch x := val.(type) {
	case string:
		return strconv.ParseInt(strings.TrimSpace(x), 10, 64)
	case float64:
		n := int64(x)
		if float64(n) != x {
			return 0, strconv.ErrSyntax
		}
		return n, nil
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	default:
		return 0, strconv.ErrSyntax
	}
}

func asFloat(val any) (float64, error) {
	switch x := val.(type) {
	case string:
		return strconv.ParseFloat(strings.TrimSpace(x), 64)
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	default:
		return 0, strconv.ErrSyntax
	}
}

func plainText(val any) string {
	switch x := val.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// CheckColumns reports a column that is not in its data set. The message
// names the file and the column, not a cell.
func CheckColumns(slots []Slot, sets map[string]*Set) error {
	seen := map[string]bool{}
	for _, slot := range slots {
		key := slot.Set + "\x00" + slot.Column
		if seen[key] {
			continue
		}
		seen[key] = true
		set := sets[slot.Set]
		if set == nil {
			return fmt.Errorf("data set %s is not loaded", slot.Set)
		}
		if !set.HasColumn(slot.Column) {
			return fmt.Errorf("data file %s: column %s does not exist", set.FileName(), slot.Column)
		}
	}
	return nil
}
