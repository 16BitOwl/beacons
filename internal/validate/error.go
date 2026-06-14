package validate

import (
	"fmt"
	"strings"
)

// ValidationError describes a single field that failed a validation rule.
type ValidationError struct {
	// Path is the full dot-separated YAML path to the field,
	// e.g. "upstreams.cf-prod.api_token".
	Path string

	// Tag is the rule that failed, e.g. "required" or "oneof".
	Tag string

	// Value is the field's actual value at the time of validation.
	Value any

	// Message is a human-readable description of the failure.
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

// ValidationErrors is a slice of ValidationError that itself implements error.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = "  " + e.Message
	}
	return fmt.Sprintf("validation failed:\n%s", strings.Join(msgs, "\n"))
}

func newError(path, tag string, value any) ValidationError {
	return ValidationError{
		Path:    path,
		Tag:     tag,
		Value:   value,
		Message: fmt.Sprintf("%s: failed rule %q (got %v)", path, tag, value),
	}
}
