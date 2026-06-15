package config

import (
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
)

const envPrefix = "BEACONS"

// overlayEnv walks v (must be a pointer to a struct) and overlays matching
// BEACONS_* environment variables onto it.
//
// Naming convention:
//   - Static struct fields use single underscores as separators, matching the yaml tag path:
//     BEACONS_DEFAULTS_TTL, BEACONS_SYNC_POLL_INTERVAL
//   - Dynamic map keys are wrapped in double underscores:
//     BEACONS_UPSTREAMS__CF_ZONE_A__TYPE
//     where CF_ZONE_A is the map key (hyphens represented as single underscores)
func overlayEnv(v any) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return
	}
	// Build an index of all BEACONS_ env vars once for the whole walk.
	env := collectEnv(envPrefix + "_")
	walkStruct(rv.Elem(), envPrefix, env)
}

// collectEnv returns all env vars whose key starts with prefix, as a map of key→value.
func collectEnv(prefix string) map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		k, v, found := strings.Cut(e, "=")
		if found && strings.HasPrefix(k, prefix) {
			m[k] = v
		}
	}
	return m
}

func walkStruct(v reflect.Value, path string, env map[string]string) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		yamlKey := yamlTagName(field)
		if yamlKey == "" || yamlKey == "-" {
			continue
		}
		fieldPath := path + strings.ToUpper(yamlKey)
		if !strings.HasSuffix(path, "__") {
			fieldPath = path + "_" + strings.ToUpper(yamlKey)
		}

		switch fv.Kind() {
		case reflect.Struct:
			walkStruct(fv, fieldPath, env)
		case reflect.Map:
			walkMap(fv, fieldPath, env)
		default:
			if val, ok := env[fieldPath]; ok && val != "" {
				prev := fv.Interface()
				setScalar(fv, val, fieldPath)
				if reflect.DeepEqual(prev, reflect.Zero(fv.Type()).Interface()) {
					slog.Debug("config set from env", "key", fieldPath, "value", fv.Interface())
				} else if !reflect.DeepEqual(prev, fv.Interface()) {
					slog.Debug("config overridden by env", "key", fieldPath, "old", prev, "new", fv.Interface())
				}
			}
		}
	}
}

// walkMap handles map[string]struct fields.
// Map keys in env vars are wrapped in double underscores: PATH__MAP_KEY__FIELD
// e.g. BEACONS_UPSTREAMS__CF_ZONE_A__TYPE where CF_ZONE_A → cf-zone-a (matched
// against existing keys) or cf_zone_a (as a new key).
func walkMap(mv reflect.Value, path string, env map[string]string) {
	if mv.IsNil() {
		mv.Set(reflect.MakeMap(mv.Type()))
	}
	elemType := mv.Type().Elem()
	if elemType.Kind() != reflect.Struct {
		return
	}

	// Env vars for this map look like: {path}__{KEY}__{FIELD...}
	// Collect all unique KEY tokens under this path.
	prefix := path + "__"
	keys := map[string]string{} // normalised token → resolved map key
	for envKey := range env {
		if !strings.HasPrefix(envKey, prefix) {
			continue
		}
		rest := strings.TrimPrefix(envKey, prefix)
		// Key token is everything up to the next __.
		token, _, found := strings.Cut(rest, "__")
		if !found || token == "" {
			continue
		}
		if _, seen := keys[token]; seen {
			continue
		}
		keys[token] = resolveMapKey(mv, token)
	}

	for token, mapKey := range keys {
		entry := mv.MapIndex(reflect.ValueOf(mapKey))
		elem := reflect.New(elemType).Elem()
		if entry.IsValid() {
			elem.Set(entry)
		}
		entryPath := prefix + token + "__"
		walkStruct(elem, entryPath, env)
		mv.SetMapIndex(reflect.ValueOf(mapKey), elem)
	}
}

// resolveMapKey finds the existing map key whose normalised form matches token,
// or returns a new lowercase key derived from the token (underscores kept as-is).
// Normalisation: lowercase, hyphens → underscores.
func resolveMapKey(mv reflect.Value, token string) string {
	norm := strings.ToLower(token)
	for _, k := range mv.MapKeys() {
		key := k.String()
		if strings.ToLower(strings.ReplaceAll(key, "-", "_")) == norm {
			return key
		}
	}
	// New key: use lowercase token directly (underscores preserved, no hyphen recovery).
	return norm
}

func setScalar(fv reflect.Value, val, key string) {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(val)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			fv.SetInt(n)
		} else {
			slog.Warn("config env var has unparseable value, ignoring",
				"key", key,
				"value", val,
				"err", err)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(val, 10, 64); err == nil {
			fv.SetUint(n)
		} else {
			slog.Warn("config env var has unparseable value, ignoring",
				"key", key,
				"value", val,
				"err", err)
		}
	case reflect.Bool:
		fv.SetBool(val == "true" || val == "1")
	case reflect.Float32, reflect.Float64:
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			fv.SetFloat(n)
		} else {
			slog.Warn("config env var has unparseable value, ignoring",
				"key", key,
				"value", val,
				"err", err)
		}
	}
}

func yamlTagName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}
