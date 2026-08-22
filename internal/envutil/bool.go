// Package envutil holds the parsers shared by everything that reads
// configuration out of the environment.
//
// It exists because "is this environment variable on?" had been re-implemented
// nine times across the tree, each copy accepting a slightly different set of
// spellings — so whether SILO_X=enabled worked depended on which subsystem read
// it. New env flags belong here; the remaining ad hoc copies should migrate as
// the code around them is touched.
package envutil

import (
	"os"
	"strings"
)

// Truthy reports whether a raw environment value means "on". Case and
// surrounding whitespace are ignored. Anything else, including an empty or
// unset value, is false — a flag has to be turned on deliberately.
func Truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

// Bool reports whether the named environment variable is set to a truthy value.
func Bool(name string) bool { return Truthy(os.Getenv(name)) }
