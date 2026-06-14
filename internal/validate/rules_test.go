// Internal tests for rules.go. Uses package validate (not validate_test) so
// unexported symbols — ruleCtx, isZero, and the individual rule functions —
// are directly accessible.
package validate

import (
	"reflect"
	"testing"
)

// field returns a ruleCtx whose Field is set to the reflect.Value of v.
// Parent is left as the zero Value since most rules do not use it.
func field(v any) ruleCtx {
	return ruleCtx{Field: reflect.ValueOf(v)}
}

// ---------------------------------------------------------------------------
// isZero
// ---------------------------------------------------------------------------

func TestIsZero_String(t *testing.T) {
	if !isZero(reflect.ValueOf("")) {
		t.Error("empty string: want true")
	}
	if isZero(reflect.ValueOf(" ")) {
		t.Error("whitespace string: want false")
	}
	if isZero(reflect.ValueOf("x")) {
		t.Error("non-empty string: want false")
	}
}

func TestIsZero_Int(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{int(0), true},
		{int(1), false},
		{int(-1), false},
		{int8(0), true},
		{int8(1), false},
		{int16(0), true},
		{int16(1), false},
		{int32(0), true},
		{int32(1), false},
		{int64(0), true},
		{int64(1), false},
	}
	for _, c := range cases {
		got := isZero(reflect.ValueOf(c.v))
		if got != c.want {
			t.Errorf("isZero(%T(%v)) = %v, want %v", c.v, c.v, got, c.want)
		}
	}
}

func TestIsZero_Uint(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{uint(0), true},
		{uint(1), false},
		{uint8(0), true},
		{uint8(1), false},
		{uint16(0), true},
		{uint16(1), false},
		{uint32(0), true},
		{uint32(1), false},
		{uint64(0), true},
		{uint64(1), false},
	}
	for _, c := range cases {
		got := isZero(reflect.ValueOf(c.v))
		if got != c.want {
			t.Errorf("isZero(%T(%v)) = %v, want %v", c.v, c.v, got, c.want)
		}
	}
}

func TestIsZero_Float(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{float32(0), true},
		{float32(1.5), false},
		{float64(0), true},
		{float64(-0.001), false},
	}
	for _, c := range cases {
		got := isZero(reflect.ValueOf(c.v))
		if got != c.want {
			t.Errorf("isZero(%T(%v)) = %v, want %v", c.v, c.v, got, c.want)
		}
	}
}

func TestIsZero_Bool(t *testing.T) {
	if !isZero(reflect.ValueOf(false)) {
		t.Error("false: want true")
	}
	if isZero(reflect.ValueOf(true)) {
		t.Error("true: want false")
	}
}

func TestIsZero_Pointer(t *testing.T) {
	var p *string
	if !isZero(reflect.ValueOf(p)) {
		t.Error("nil pointer: want true")
	}
	s := "hello"
	if isZero(reflect.ValueOf(&s)) {
		t.Error("non-nil pointer: want false")
	}
}

func TestIsZero_Slice(t *testing.T) {
	if !isZero(reflect.ValueOf([]string(nil))) {
		t.Error("nil slice: want true")
	}
	if !isZero(reflect.ValueOf([]string{})) {
		t.Error("empty slice: want true")
	}
	if isZero(reflect.ValueOf([]string{"x"})) {
		t.Error("non-empty slice: want false")
	}
}

func TestIsZero_Map(t *testing.T) {
	if !isZero(reflect.ValueOf(map[string]string(nil))) {
		t.Error("nil map: want true")
	}
	if !isZero(reflect.ValueOf(map[string]string{})) {
		t.Error("empty map: want true")
	}
	if isZero(reflect.ValueOf(map[string]string{"k": "v"})) {
		t.Error("non-empty map: want false")
	}
}

func TestIsZero_OtherKindReturnsFalse(t *testing.T) {
	// Struct kind — not handled explicitly, falls through to default false.
	type S struct{}
	if isZero(reflect.ValueOf(S{})) {
		t.Error("struct: want false (unhandled kind)")
	}
}

// ---------------------------------------------------------------------------
// ruleRequired
// ---------------------------------------------------------------------------

func TestRuleRequired(t *testing.T) {
	// required passes when the value is non-zero.
	if !ruleRequired(field("hello"), "") {
		t.Error("non-empty string: want true")
	}
	if !ruleRequired(field(1), "") {
		t.Error("non-zero int: want true")
	}
	// required fails when the value is zero.
	if ruleRequired(field(""), "") {
		t.Error("empty string: want false")
	}
	if ruleRequired(field(0), "") {
		t.Error("zero int: want false")
	}
	var p *string
	if ruleRequired(field(p), "") {
		t.Error("nil pointer: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleOneof
// ---------------------------------------------------------------------------

func TestRuleOneof_StringInList(t *testing.T) {
	for _, v := range []string{"cloudflare", "pihole"} {
		if !ruleOneof(field(v), "cloudflare pihole") {
			t.Errorf("%q: want true", v)
		}
	}
}

func TestRuleOneof_StringNotInList(t *testing.T) {
	for _, v := range []string{"", "unknown", "Cloudflare", "PIHOLE", "cloudflare "} {
		if ruleOneof(field(v), "cloudflare pihole") {
			t.Errorf("%q: want false", v)
		}
	}
}

func TestRuleOneof_SingleOption(t *testing.T) {
	if !ruleOneof(field("only"), "only") {
		t.Error("exact single match: want true")
	}
	if ruleOneof(field("other"), "only") {
		t.Error("non-match single option: want false")
	}
}

func TestRuleOneof_NonStringKindReturnsFalse(t *testing.T) {
	if ruleOneof(field(1), "1 2 3") {
		t.Error("int kind: want false")
	}
	if ruleOneof(field(true), "true false") {
		t.Error("bool kind: want false")
	}
}

func TestRuleOneof_CaseSensitive(t *testing.T) {
	if ruleOneof(field("DOCKER"), "docker yaml") {
		t.Error("uppercase value should not match lowercase option")
	}
	if ruleOneof(field("Docker"), "docker yaml") {
		t.Error("mixed-case value should not match lowercase option")
	}
}

// ---------------------------------------------------------------------------
// ruleGt
// ---------------------------------------------------------------------------

func TestRuleGt_Int(t *testing.T) {
	cases := []struct {
		v     int
		param string
		want  bool
	}{
		{1, "0", true},
		{0, "0", false},
		{-1, "0", false},
		{100, "99", true},
		{99, "99", false},
		{5, "-1", true},  // 5 > -1
		{-1, "-2", true}, // -1 > -2
	}
	for _, c := range cases {
		got := ruleGt(field(c.v), c.param)
		if got != c.want {
			t.Errorf("ruleGt(%d, %q) = %v, want %v", c.v, c.param, got, c.want)
		}
	}
}

func TestRuleGt_Uint(t *testing.T) {
	if !ruleGt(field(uint(1)), "0") {
		t.Error("uint(1) > 0: want true")
	}
	if ruleGt(field(uint(0)), "0") {
		t.Error("uint(0) > 0: want false")
	}
}

func TestRuleGt_Float(t *testing.T) {
	if !ruleGt(field(float64(1.0)), "0") {
		t.Error("float64(1.0) > 0: want true")
	}
	if ruleGt(field(float64(0.0)), "0") {
		t.Error("float64(0.0) > 0: want false")
	}
}

func TestRuleGt_InvalidParamReturnsFalse(t *testing.T) {
	if ruleGt(field(5), "not-a-number") {
		t.Error("invalid param: want false")
	}
	if ruleGt(field(5), "") {
		t.Error("empty param: want false")
	}
}

func TestRuleGt_NonNumericKindReturnsFalse(t *testing.T) {
	if ruleGt(field("hello"), "0") {
		t.Error("string kind: want false")
	}
	if ruleGt(field(true), "0") {
		t.Error("bool kind: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleMin
// ---------------------------------------------------------------------------

func TestRuleMin_String(t *testing.T) {
	cases := []struct {
		v     string
		param string
		want  bool
	}{
		{"abc", "3", true},
		{"ab", "3", false},
		{"abcd", "3", true},
		{"", "0", true},
		{"", "1", false},
	}
	for _, c := range cases {
		got := ruleMin(field(c.v), c.param)
		if got != c.want {
			t.Errorf("ruleMin(%q, %q) = %v, want %v", c.v, c.param, got, c.want)
		}
	}
}

func TestRuleMin_Int(t *testing.T) {
	cases := []struct {
		v     int
		param string
		want  bool
	}{
		{0, "0", true},
		{-1, "0", false},
		{1, "0", true},
		{5, "5", true},
		{4, "5", false},
	}
	for _, c := range cases {
		got := ruleMin(field(c.v), c.param)
		if got != c.want {
			t.Errorf("ruleMin(%d, %q) = %v, want %v", c.v, c.param, got, c.want)
		}
	}
}

func TestRuleMin_Uint(t *testing.T) {
	if !ruleMin(field(uint(0)), "0") {
		t.Error("uint(0) >= 0: want true")
	}
	if !ruleMin(field(uint(5)), "5") {
		t.Error("uint(5) >= 5: want true")
	}
}

func TestRuleMin_Float(t *testing.T) {
	if !ruleMin(field(float64(1.5)), "1") {
		t.Error("float(1.5) >= 1: want true")
	}
	if ruleMin(field(float64(0.5)), "1") {
		t.Error("float(0.5) >= 1: want false")
	}
}

func TestRuleMin_Slice(t *testing.T) {
	if !ruleMin(field([]string{"a", "b"}), "2") {
		t.Error("slice len 2 >= 2: want true")
	}
	if ruleMin(field([]string{"a"}), "2") {
		t.Error("slice len 1 >= 2: want false")
	}
	if !ruleMin(field([]string{}), "0") {
		t.Error("empty slice len 0 >= 0: want true")
	}
}

func TestRuleMin_Map(t *testing.T) {
	if !ruleMin(field(map[string]int{"a": 1, "b": 2}), "2") {
		t.Error("map len 2 >= 2: want true")
	}
	if ruleMin(field(map[string]int{"a": 1}), "2") {
		t.Error("map len 1 >= 2: want false")
	}
}

func TestRuleMin_InvalidParamReturnsFalse(t *testing.T) {
	if ruleMin(field("hello"), "not-a-number") {
		t.Error("invalid param: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleMax
// ---------------------------------------------------------------------------

func TestRuleMax_String(t *testing.T) {
	cases := []struct {
		v     string
		param string
		want  bool
	}{
		{"hello", "5", true},
		{"toolong", "5", false},
		{"", "0", true},
		{"x", "0", false},
	}
	for _, c := range cases {
		got := ruleMax(field(c.v), c.param)
		if got != c.want {
			t.Errorf("ruleMax(%q, %q) = %v, want %v", c.v, c.param, got, c.want)
		}
	}
}

func TestRuleMax_Int(t *testing.T) {
	cases := []struct {
		v     int
		param string
		want  bool
	}{
		{100, "100", true},
		{101, "100", false},
		{0, "100", true},
		{-1, "0", true},
	}
	for _, c := range cases {
		got := ruleMax(field(c.v), c.param)
		if got != c.want {
			t.Errorf("ruleMax(%d, %q) = %v, want %v", c.v, c.param, got, c.want)
		}
	}
}

func TestRuleMax_Uint(t *testing.T) {
	if !ruleMax(field(uint(5)), "5") {
		t.Error("uint(5) <= 5: want true")
	}
	if ruleMax(field(uint(6)), "5") {
		t.Error("uint(6) <= 5: want false")
	}
}

func TestRuleMax_Float(t *testing.T) {
	if !ruleMax(field(float64(1.0)), "1") {
		t.Error("float(1.0) <= 1: want true")
	}
	if ruleMax(field(float64(1.1)), "1") {
		t.Error("float(1.1) <= 1: want false")
	}
}

func TestRuleMax_Slice(t *testing.T) {
	if !ruleMax(field([]string{"a", "b"}), "3") {
		t.Error("slice len 2 <= 3: want true")
	}
	if ruleMax(field([]string{"a", "b", "c", "d"}), "3") {
		t.Error("slice len 4 <= 3: want false")
	}
}

func TestRuleMax_Map(t *testing.T) {
	if !ruleMax(field(map[string]int{"a": 1}), "2") {
		t.Error("map len 1 <= 2: want true")
	}
	if ruleMax(field(map[string]int{"a": 1, "b": 2, "c": 3}), "2") {
		t.Error("map len 3 <= 2: want false")
	}
}

func TestRuleMax_InvalidParamReturnsFalse(t *testing.T) {
	if ruleMax(field("hello"), "not-a-number") {
		t.Error("invalid param: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleURL
// ---------------------------------------------------------------------------

func TestRuleURL_Valid(t *testing.T) {
	for _, v := range []string{
		"http://example.com",
		"https://example.com",
		"http://localhost:8080",
		"https://pihole.home/api",
		"http://192.168.1.1:8080",
		"https://example.com/path?q=1#frag",
	} {
		if !ruleURL(field(v), "") {
			t.Errorf("%q: want true", v)
		}
	}
}

func TestRuleURL_Invalid(t *testing.T) {
	for _, v := range []string{
		"",
		"not-a-url",
		"//missing-scheme.com",
		"http://",
		"ftp://",
		"just-a-hostname",
		"/relative/path",
	} {
		if ruleURL(field(v), "") {
			t.Errorf("%q: want false", v)
		}
	}
}

func TestRuleURL_NonStringKindReturnsFalse(t *testing.T) {
	if ruleURL(field(42), "") {
		t.Error("int kind: want false")
	}
	if ruleURL(field(true), "") {
		t.Error("bool kind: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleHostnamePort
// ---------------------------------------------------------------------------

func TestRuleHostnamePort_Valid(t *testing.T) {
	for _, v := range []string{
		":9090",
		"localhost:8080",
		"0.0.0.0:9090",
		"example.com:443",
		"192.168.1.1:80",
	} {
		if !ruleHostnamePort(field(v), "") {
			t.Errorf("%q: want true", v)
		}
	}
}

func TestRuleHostnamePort_Invalid(t *testing.T) {
	for _, v := range []string{
		"",
		"noporthere",
		"http://localhost:8080",
		"localhost",
	} {
		if ruleHostnamePort(field(v), "") {
			t.Errorf("%q: want false", v)
		}
	}
}

func TestRuleHostnamePort_NonStringKindReturnsFalse(t *testing.T) {
	if ruleHostnamePort(field(9090), "") {
		t.Error("int kind: want false")
	}
}

// ---------------------------------------------------------------------------
// ruleRequiredIf (no-op registered function)
// ---------------------------------------------------------------------------

func TestRuleRequiredIf_NoOpAlwaysTrue(t *testing.T) {
	// The registered ruleRequiredIf is a no-op placeholder — the real logic
	// is handled separately in applyRules. Verify it always returns true
	// so it never adds a spurious error when called through the registry.
	if !ruleRequiredIf(field(""), "") {
		t.Error("no-op required_if: want true")
	}
	if !ruleRequiredIf(field(0), "Type cloudflare") {
		t.Error("no-op required_if with param: want true")
	}
}

// ---------------------------------------------------------------------------
// Rules registry
// ---------------------------------------------------------------------------

func TestRulesRegistry_AllExpectedRulesPresent(t *testing.T) {
	expected := []string{
		"required",
		"oneof",
		"gt",
		"min",
		"max",
		"url",
		"hostname_port",
		"required_if",
	}
	for _, name := range expected {
		if _, ok := rules[name]; !ok {
			t.Errorf("rule %q not registered in rules map", name)
		}
	}
}

func TestRulesRegistry_UnknownRuleNotPresent(t *testing.T) {
	for _, name := range []string{"", "Required", "REQUIRED", "nonexistent"} {
		if _, ok := rules[name]; ok {
			t.Errorf("rule %q should not be registered", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Boundary conditions shared across numeric rules
// ---------------------------------------------------------------------------

func TestNumericRules_BoundaryExact(t *testing.T) {
	// gt=5: exactly 5 fails (not strictly greater), 6 passes.
	if ruleGt(field(5), "5") {
		t.Error("gt=5 with value 5: want false (not strictly greater)")
	}
	if !ruleGt(field(6), "5") {
		t.Error("gt=5 with value 6: want true")
	}

	// min=5: exactly 5 passes.
	if !ruleMin(field(5), "5") {
		t.Error("min=5 with value 5: want true")
	}
	if ruleMin(field(4), "5") {
		t.Error("min=5 with value 4: want false")
	}

	// max=5: exactly 5 passes.
	if !ruleMax(field(5), "5") {
		t.Error("max=5 with value 5: want true")
	}
	if ruleMax(field(6), "5") {
		t.Error("max=5 with value 6: want false")
	}
}
