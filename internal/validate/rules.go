package validate

import (
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// ruleCtx is passed to every rule function.
// Field is the value being validated; Parent is the enclosing struct (used by
// cross-field rules like required_if).
type ruleCtx struct {
	Field  reflect.Value
	Parent reflect.Value
}

// ruleFn is the signature for a validation rule.
type ruleFn func(ctx ruleCtx, param string) bool

// rules is the registry of built-in rule names to their implementations.
var rules = map[string]ruleFn{
	"required":      ruleRequired,
	"oneof":         ruleOneof,
	"gt":            ruleGt,
	"min":           ruleMin,
	"max":           ruleMax,
	"url":           ruleURL,
	"hostname_port": ruleHostnamePort,
}

// isZero reports whether v is the zero value for its type.
func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	}
	return false
}

// ruleRequired fails if the field is the zero value for its type.
func ruleRequired(ctx ruleCtx, _ string) bool {
	return !isZero(ctx.Field)
}

// ruleOneof fails if the string field value is not in the space-separated list.
// e.g. validate:"oneof=cloudflare pihole"
func ruleOneof(ctx ruleCtx, param string) bool {
	if ctx.Field.Kind() != reflect.String {
		return false
	}
	v := ctx.Field.String()
	for _, opt := range strings.Fields(param) {
		if v == opt {
			return true
		}
	}
	return false
}

// ruleGt fails if the integer field value is not greater than param.
// e.g. validate:"gt=0"
func ruleGt(ctx ruleCtx, param string) bool {
	n, err := strconv.ParseInt(param, 10, 64)
	if err != nil {
		return false
	}
	switch ctx.Field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ctx.Field.Int() > n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(ctx.Field.Uint()) > n
	case reflect.Float32, reflect.Float64:
		return ctx.Field.Float() > float64(n)
	}
	return false
}

// ruleMin fails if a string is shorter than param characters, or a number is
// less than param.
// e.g. validate:"min=1"
func ruleMin(ctx ruleCtx, param string) bool {
	n, err := strconv.ParseInt(param, 10, 64)
	if err != nil {
		return false
	}
	switch ctx.Field.Kind() {
	case reflect.String:
		return int64(len(ctx.Field.String())) >= n
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ctx.Field.Int() >= n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(ctx.Field.Uint()) >= n
	case reflect.Float32, reflect.Float64:
		return ctx.Field.Float() >= float64(n)
	case reflect.Slice, reflect.Map:
		return int64(ctx.Field.Len()) >= n
	}
	return false
}

// ruleMax fails if a string is longer than param characters, or a number exceeds
// param.
// e.g. validate:"max=255"
func ruleMax(ctx ruleCtx, param string) bool {
	n, err := strconv.ParseInt(param, 10, 64)
	if err != nil {
		return false
	}
	switch ctx.Field.Kind() {
	case reflect.String:
		return int64(len(ctx.Field.String())) <= n
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ctx.Field.Int() <= n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(ctx.Field.Uint()) <= n
	case reflect.Float32, reflect.Float64:
		return ctx.Field.Float() <= float64(n)
	case reflect.Slice, reflect.Map:
		return int64(ctx.Field.Len()) <= n
	}
	return false
}

// ruleURL fails if the string field is not a valid URL with a scheme and host.
// e.g. validate:"url"
func ruleURL(ctx ruleCtx, _ string) bool {
	if ctx.Field.Kind() != reflect.String {
		return false
	}
	u, err := url.Parse(ctx.Field.String())
	return err == nil && u.Scheme != "" && u.Host != ""
}

// ruleHostnamePort fails if the string field is not a valid "host:port" or
// ":port" address.
// e.g. validate:"hostname_port"
func ruleHostnamePort(ctx ruleCtx, _ string) bool {
	if ctx.Field.Kind() != reflect.String {
		return false
	}
	_, _, err := net.SplitHostPort(ctx.Field.String())
	return err == nil
}

// ruleRequiredIf is handled specially in the walker (it needs the parent
// struct), but is registered here as a no-op so unknown-rule checks pass.
// The actual logic lives in applyRules.
func ruleRequiredIf(_ ruleCtx, _ string) bool { return true }

func init() {
	rules["required_if"] = ruleRequiredIf
}
