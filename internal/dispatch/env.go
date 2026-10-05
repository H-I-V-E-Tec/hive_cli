package dispatch

import "strings"

// Env returns environ with HIVE_LAUNCHER set to launcher, so products can
// register the stable launcher path (not their versioned binary) in agents.
func Env(environ []string, launcher string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "HIVE_LAUNCHER=") {
			out = append(out, kv)
		}
	}
	return append(out, "HIVE_LAUNCHER="+launcher)
}
