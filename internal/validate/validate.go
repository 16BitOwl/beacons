// Package validate provides a lightweight struct validator driven by
// `validate` struct tags. It is intentionally dependency-free.
//
// Supported rules (comma-separated in the tag):
//
//	required           — field must not be the zero value
//	omitempty          — skip remaining rules if field is zero
//	oneof=a b c        — string must be one of the listed values
//	gt=N               — integer/float must be greater than N
//	min=N              — string length or numeric value must be >= N
//	max=N              — string length or numeric value must be <= N
//	url                — string must be a valid URL with scheme and host
//	hostname_port      — string must be a valid "host:port" or ":port"
//	required_if=F v    — required if sibling field F equals value v
//
// Paths in errors are built from yaml struct tags (falling back to lowercase
// field names) so they map directly to config file keys.
//
// Structs that implement Validatable can inject custom cross-field logic that
// runs after tag-based validation.
package validate

import (
	"reflect"
	"strings"
)

// Validatable may be implemented by any struct that needs custom cross-field
// validation logic beyond what tags can express. BeaconsValidate is called
// after tag-based validation with the current dot-path prefix.
type Validatable interface {
	BeaconsValidate(path string) ValidationErrors
}

// Struct validates the exported fields of v using validate struct tags.
// All failures are collected and returned together as a ValidationErrors.
// Returns nil if validation passes.
func Struct(v any) error {
	return StructWithPrefix(v, "")
}

// StructWithPrefix is like Struct but prepends prefix to all error paths.
// Useful when validating records from external sources (Docker labels, YAML
// files) where the caller has meaningful location context to include.
func StructWithPrefix(v any, prefix string) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	errs := walkStruct(rv, prefix)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// walkStruct iterates the exported fields of a struct value, applies validate
// tags, and recurses into nested structs, maps, and slices.
func walkStruct(rv reflect.Value, path string) ValidationErrors {
	rt := rv.Type()
	var errs ValidationErrors

	for i := range rt.NumField() {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}

		fv := rv.Field(i)
		fpath := fieldPath(path, field)

		// Dereference pointer fields; skip nil pointers entirely.
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				// Still apply validate tags (e.g. required_if) on the pointer itself.
				errs = append(errs, applyRules(fv, rv, fpath, field.Tag.Get("validate"))...)
				continue
			}
			fv = fv.Elem()
		}

		// Apply tag-based rules on the field.
		errs = append(errs, applyRules(fv, rv, fpath, field.Tag.Get("validate"))...)

		// Recurse based on kind.
		switch fv.Kind() {
		case reflect.Struct:
			errs = append(errs, walkStruct(fv, fpath)...)
		case reflect.Map:
			errs = append(errs, walkMap(fv, fpath)...)
		case reflect.Slice:
			errs = append(errs, walkSlice(fv, fpath)...)
		}
	}

	// Custom cross-field validation. Struct() may be called with a non-pointer
	// value (unaddressable), so guard Addr() and fall back to the value receiver.
	if rv.CanAddr() {
		if val, ok := rv.Addr().Interface().(Validatable); ok {
			errs = append(errs, val.BeaconsValidate(path)...)
		}
	} else if val, ok := rv.Interface().(Validatable); ok {
		errs = append(errs, val.BeaconsValidate(path)...)
	}

	return errs
}

// walkMap iterates a map whose values are structs (or pointers to structs) and
// recurses into each value, using the map key as the next path segment.
func walkMap(rv reflect.Value, path string) ValidationErrors {
	var errs ValidationErrors
	for _, key := range rv.MapKeys() {
		seg := joinPath(path, key.String())
		mv := rv.MapIndex(key)

		// Maps return unaddressable values; copy to an addressable one.
		if mv.Kind() == reflect.Interface {
			mv = mv.Elem()
		}
		if mv.Kind() == reflect.Pointer {
			if mv.IsNil() {
				continue
			}
			mv = mv.Elem()
		}
		if mv.Kind() == reflect.Struct {
			// MapIndex values are not addressable, so we copy to a new pointer.
			ptr := reflect.New(mv.Type())
			ptr.Elem().Set(mv)
			errs = append(errs, walkStruct(ptr.Elem(), seg)...)
		}
	}
	return errs
}

// walkSlice iterates a slice and recurses into each struct element.
func walkSlice(rv reflect.Value, path string) ValidationErrors {
	var errs ValidationErrors
	for i := range rv.Len() {
		seg := path + "[" + itoa(i) + "]"
		sv := rv.Index(i)
		if sv.Kind() == reflect.Pointer {
			if sv.IsNil() {
				continue
			}
			sv = sv.Elem()
		}
		if sv.Kind() == reflect.Struct {
			errs = append(errs, walkStruct(sv, seg)...)
		}
	}
	return errs
}

// applyRules parses the validate tag and runs each rule against fv.
// parent is the enclosing struct, used by required_if.
func applyRules(fv, parent reflect.Value, path, tag string) ValidationErrors {
	if tag == "" {
		return nil
	}

	var errs ValidationErrors

	for _, rule := range strings.Split(tag, ",") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}

		// omitempty: if the field is zero, stop processing further rules.
		if rule == "omitempty" {
			if isZero(fv) {
				return errs
			}
			continue
		}

		name, param, _ := strings.Cut(rule, "=")

		// required_if is handled here because it needs parent context.
		if name == "required_if" {
			parts := strings.SplitN(param, " ", 2)
			if len(parts) != 2 {
				continue
			}
			siblingName, siblingVal := parts[0], parts[1]
			sf := parent.FieldByName(siblingName)
			if !sf.IsValid() {
				continue
			}
			if sf.Kind() == reflect.String && sf.String() == siblingVal {
				if isZero(fv) {
					errs = append(errs, newError(path, rule, fv.Interface()))
				}
			}
			continue
		}

		fn, ok := rules[name]
		if !ok {
			continue
		}
		ctx := ruleCtx{Field: fv, Parent: parent}
		if !fn(ctx, param) {
			errs = append(errs, newError(path, rule, fv.Interface()))
		}
	}

	return errs
}

// fieldPath returns the dot-separated path for a struct field, using the yaml
// tag name as the segment (falling back to the lowercase field name).
// Inline fields (yaml:",inline") contribute no segment of their own.
func fieldPath(parent string, f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return joinPath(parent, strings.ToLower(f.Name))
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		// yaml:",inline" — no segment; the parent path is used when recursing.
		return parent
	}
	return joinPath(parent, name)
}

// joinPath concatenates two path segments with a dot, handling empty prefix.
func joinPath(prefix, segment string) string {
	if prefix == "" {
		return segment
	}
	return prefix + "." + segment
}

// itoa converts an integer to its decimal string representation without
// importing strconv or fmt in this file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
