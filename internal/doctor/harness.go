package doctor

import (
	"context"
	"fmt"
	"net/http"
)

// The Harness checks are the only place doctor talks to Harness, and only
// when the user passes -harness (NFR-05). The credential variable names
// below are placeholders until the --move-to-harness bridge defines the
// real ones.
var harnessCredentialVars = []string{"VEGALOAD_HARNESS_API_KEY", "HARNESS_API_KEY"}

const defaultHarnessURL = "https://app.harness.io"

func harnessChecks(env Env, opt Options) []Check {
	if !opt.Harness {
		return []Check{{ID: "harness.off", Category: "harness", Name: "Harness RT", Run: func(context.Context) Result {
			return result(Skip, "not checked. Pass -harness to verify Harness RT access. Without it, doctor makes no call to Harness.")
		}}}
	}
	return []Check{
		{ID: "harness.credentials", Category: "harness", Name: "Harness credentials", Run: func(context.Context) Result {
			for _, name := range harnessCredentialVars {
				if env.Getenv(name) != "" {
					return result(Pass, "an API key is set in $"+name)
				}
			}
			r := result(Fail, "no Harness API key found")
			r.Fix = "Set " + harnessCredentialVars[0] + " to a Harness API key. Sign-in from the CLI arrives with `--move-to-harness`."
			return r
		}},
		{ID: "harness.reachable", Category: "harness", Name: "Harness reachable", Run: func(ctx context.Context) Result {
			base := env.Getenv("VEGALOAD_HARNESS_URL")
			if base == "" {
				base = defaultHarnessURL
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
			if err != nil {
				return result(Fail, "bad Harness URL "+base+": "+err.Error())
			}
			req.Header.Set("User-Agent", "vegaload-doctor/"+env.Version)
			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				r := result(Fail, "cannot reach "+base+": "+err.Error())
				r.Fix = "Check your network, VPN, and proxy settings."
				return r
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				return result(Warn, fmt.Sprintf("%s answered HTTP %d", base, resp.StatusCode))
			}
			return result(Pass, fmt.Sprintf("%s answered HTTP %d", base, resp.StatusCode))
		}},
		{ID: "harness.token", Category: "harness", Name: "Credential validity", Run: func(context.Context) Result {
			return result(Skip, "a key can only be verified once the Harness bridge ships")
		}},
	}
}
