// inputs.go wires FR-CLI-17's flags (-data, -env, -secret-env) into a run.
// `run` and `validate` share them, so a scenario that validates with a set
// of flags runs with the same ones.
package main

import (
	"flag"
	"fmt"

	"github.com/vegaload/vegaload/internal/inputs"
)

// scriptInputFlags holds the raw values of the three flags until the
// files are read and the variables are looked up.
type scriptInputFlags struct {
	data      repeatedFlags
	env       repeatedFlags
	secretEnv repeatedFlags
}

func (f *scriptInputFlags) register(fs *flag.FlagSet) {
	fs.Var(&f.data, "data", "a CSV (header row) or JSON (array of objects) file whose rows scripts read as data.NAME.next() or data.NAME.random(); NAME is the file name without its extension, or give one as name=path (repeatable)")
	fs.Var(&f.env, "env", "an environment variable scripts may read as env.NAME; a comma-separated list is fine (repeatable). Only variables named here, or with -secret-env, are visible to scripts")
	fs.Var(&f.secretEnv, "secret-env", "like -env, but the value is a secret: it is removed from the summary, JSON, audit log and console output (repeatable)")
}

func (f *scriptInputFlags) used() bool {
	return len(f.data) > 0 || len(f.env) > 0 || len(f.secretEnv) > 0
}

// resolve reads the data files and looks up the variables, and stores the
// result on cfg. It must run before any load is sent, so a missing file or
// unset variable is a usage error.
func (f *scriptInputFlags) resolve(cfg *runConfig) error {
	if !f.used() {
		return nil
	}
	if cfg.ScenarioPath == "" {
		return fmt.Errorf("-data, -env and -secret-env are for scenario files; they do nothing with -target and -protocol")
	}
	sets, err := inputs.ParseData([]string(f.data))
	if err != nil {
		return err
	}
	env, err := inputs.ResolveEnv([]string(f.env), []string(f.secretEnv))
	if err != nil {
		return err
	}
	if in := inputs.New(env, sets); in != nil {
		cfg.Inputs = in // not assigned when nil: a nil *Inputs inside the interface would not compare equal to nil
	}
	return nil
}
