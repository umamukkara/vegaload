package doctor

import "fmt"

// plan assembles every check for the given options, in report order.
func plan(env Env, opt Options) ([]Check, error) {
	checks := coreChecks(env)
	hc, err := hostChecks(env, opt)
	if err != nil {
		return nil, err
	}
	checks = append(checks, hc...)
	checks = append(checks, targetChecks(env, opt)...)
	if opt.Smoke {
		checks = append(checks, smokeCheck(env, opt))
	}
	checks = append(checks, harnessChecks(env, opt)...)

	seen := map[string]bool{}
	for _, c := range checks {
		if seen[c.ID] {
			return nil, fmt.Errorf("internal error: duplicate check ID %q", c.ID)
		}
		seen[c.ID] = true
	}
	return checks, nil
}
