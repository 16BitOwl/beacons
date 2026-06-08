package envutil

import (
	"fmt"
	"os"
	"strings"
)

// Expand substitutes ${VAR} and $VAR references in s from the environment.
// Returns an error listing all variables that are referenced but not set.
func Expand(s string) (string, error) {
	var missing []string
	expanded := os.Expand(s, func(key string) string {
		val, ok := os.LookupEnv(key)
		if !ok {
			missing = append(missing, key)
			return ""
		}
		return val
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return expanded, nil
}

// ExpandLenient substitutes ${VAR} and $VAR references, leaving missing
// variables as empty strings without returning an error.
func ExpandLenient(s string) string {
	return os.Expand(s, func(key string) string {
		val, _ := os.LookupEnv(key)
		return val
	})
}
