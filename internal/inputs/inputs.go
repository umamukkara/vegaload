// Package inputs gives scenario scripts the data and settings they read
// from outside the script (FR-CLI-17): data files whose rows feed virtual
// users, and environment variables the run exposes by name.
//
// A script can only read an environment variable the run named with -env
// or -secret-env. Nothing else in the process environment is reachable
// through the `env` global, so a scenario written by someone else (or by an
// agent) cannot sweep up unrelated secrets. A value named with -secret-env
// is registered with internal/secrets, which keeps it out of the report,
// the JSON output, the audit record and logs.
package inputs

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/vegaload/vegaload/internal/scripting/netapi"
	"github.com/vegaload/vegaload/internal/secrets"
)

// bom is the byte order mark some spreadsheet programs put at the start of a CSV file.
const bom = "\xef\xbb\xbf"

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Set is one data file's rows.
type Set struct {
	name string
	rows []map[string]any
	next atomic.Uint64
}

var _ netapi.Dataset = (*Set)(nil)

// Next returns the next row in file order, shared by every caller, and
// starts again from the first row after the last.
func (s *Set) Next() map[string]any {
	i := s.next.Add(1) - 1
	return s.rows[i%uint64(len(s.rows))]
}

// Random returns any row.
func (s *Set) Random() map[string]any { return s.rows[rand.Intn(len(s.rows))] }

// Len is the number of rows.
func (s *Set) Len() int { return len(s.rows) }

// LoadSet reads a CSV file (a header row, then one row per line) or a JSON
// file (an array of objects). The extension picks the format.
func LoadSet(name, path string) (*Set, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("data file %s: %w", path, err)
	}
	defer f.Close()

	var rows []map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		rows, err = readCSV(f)
	case ".json":
		rows, err = readJSON(f)
	default:
		return nil, fmt.Errorf("data file %s: want a .csv or .json file", path)
	}
	if err != nil {
		return nil, fmt.Errorf("data file %s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("data file %s has no rows", path)
	}
	return &Set{name: name, rows: rows}, nil
}

func readCSV(r io.Reader) ([]map[string]any, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	for i, h := range header {
		header[i] = strings.TrimPrefix(strings.TrimSpace(h), bom)
		if header[i] == "" {
			return nil, fmt.Errorf("column %d of the header row has no name", i+1)
		}
	}
	var rows []map[string]any
	for n, rec := range records[1:] {
		if len(rec) != len(header) {
			return nil, fmt.Errorf("line %d has %d fields, the header has %d", n+2, len(rec), len(header))
		}
		row := make(map[string]any, len(header))
		for i, h := range header {
			row[h] = rec[i]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readJSON(r io.Reader) ([]map[string]any, error) {
	var rows []map[string]any
	dec := json.NewDecoder(r)
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("want a JSON array of objects: %w", err)
	}
	return rows, nil
}

// ParseData loads each -data value. A value is a path, or name=path. A bare
// path is named after the file, without its extension, so users.csv is
// `data.users`. Names must be usable as an identifier in a script.
func ParseData(specs []string) (map[string]*Set, error) {
	out := map[string]*Set{}
	for _, spec := range specs {
		name, path := "", spec
		if i := strings.Index(spec, "="); i > 0 {
			name, path = spec[:i], spec[i+1:]
		}
		if name == "" {
			base := filepath.Base(path)
			name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		if !identRE.MatchString(name) {
			return nil, fmt.Errorf("-data %q: the name %q is not usable in a script (letters, digits and _, not starting with a digit); give one with name=path", spec, name)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("-data: two files are named %q; give one a different name with name=path", name)
		}
		set, err := LoadSet(name, path)
		if err != nil {
			return nil, fmt.Errorf("-data: %w", err)
		}
		out[name] = set
	}
	return out, nil
}

// ResolveEnv reads the named environment variables. Names in secret are
// also registered as secrets. A name may be listed in both; it is then a
// secret. Each name may be a comma-separated list. A variable that is not
// set is an error, so a typo shows before any load is sent.
func ResolveEnv(plain, secret []string) (map[string]string, error) {
	out := map[string]string{}
	add := func(flag string, list []string, isSecret bool) error {
		for _, item := range list {
			for _, name := range strings.Split(item, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if !identRE.MatchString(name) {
					return fmt.Errorf("%s %q: not a usable environment variable name", flag, name)
				}
				val, ok := os.LookupEnv(name)
				if !ok {
					return fmt.Errorf("%s %s: the environment variable is not set", flag, name)
				}
				out[name] = val
				if isSecret {
					secrets.Register(val)
				}
			}
		}
		return nil
	}
	if err := add("-env", plain, false); err != nil {
		return nil, err
	}
	if err := add("-secret-env", secret, true); err != nil {
		return nil, err
	}
	return out, nil
}

// Inputs implements netapi.Inputs.
type Inputs struct {
	env  map[string]string
	sets map[string]*Set
}

var _ netapi.Inputs = (*Inputs)(nil)

// New returns Inputs for the given variables and data files, or nil when
// there are none, which is how the scripting runtimes know to expose
// nothing.
func New(env map[string]string, sets map[string]*Set) *Inputs {
	if len(env) == 0 && len(sets) == 0 {
		return nil
	}
	return &Inputs{env: env, sets: sets}
}

// Every method works on a nil *Inputs, which exposes nothing, so passing one
// where a netapi.Inputs is wanted is safe.

// Env returns an exposed variable.
func (in *Inputs) Env(name string) (string, bool) {
	if in == nil {
		return "", false
	}
	v, ok := in.env[name]
	return v, ok
}

// EnvNames lists the exposed variable names, sorted.
func (in *Inputs) EnvNames() []string {
	if in == nil {
		return nil
	}
	return sortedKeys(in.env)
}

// Dataset returns the data file named name.
func (in *Inputs) Dataset(name string) (netapi.Dataset, bool) {
	if in == nil {
		return nil, false
	}
	s, ok := in.sets[name]
	if !ok {
		return nil, false
	}
	return s, true
}

// DatasetNames lists the data file names, sorted.
func (in *Inputs) DatasetNames() []string {
	if in == nil {
		return nil
	}
	return sortedKeys(in.sets)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
