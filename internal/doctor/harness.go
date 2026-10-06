package doctor

import (
	"context"
)

// harnessChecks are a placeholder until `--move-to-harness` exists.
// They never contact Harness (NFR-05): even `-harness` only reports that
// the real check is not implemented yet. Credential names are not final.
func harnessChecks(_ Env, opt Options) []Check {
	if !opt.Harness {
		return []Check{{ID: "harness.off", Category: "harness", Name: "Harness RT", Run: func(context.Context) Result {
			return result(Skip, "not checked. Pass -harness to see the placeholder Harness RT check. Doctor never calls Harness unless you pass that flag, and even then it makes no network call until the bridge ships.")
		}}}
	}
	return []Check{{ID: "harness.placeholder", Category: "harness", Name: "Harness RT (placeholder)", Run: func(context.Context) Result {
		r := result(Skip, "placeholder: `--move-to-harness` is not implemented, so doctor cannot verify Harness RT credentials or reachability. No network call was made.")
		r.Fix = "Omit -harness until the Harness bridge ships. When it does, this check will use the real credential and API names."
		return r
	}}}
}
