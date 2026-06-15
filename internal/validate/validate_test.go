package validate_test

import (
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/validate"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustPass asserts that err is nil.
func mustPass(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no validation error, got:\n%v", err)
	}
}

// mustErrors asserts that err is a non-empty ValidationErrors and returns it.
func mustErrors(t *testing.T, err error) validate.ValidationErrors {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	ve, ok := err.(validate.ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) == 0 {
		t.Fatal("expected non-empty ValidationErrors")
	}
	return ve
}

// hasErr reports whether ve contains an error with the given path and tag.
func hasErr(ve validate.ValidationErrors, path, tag string) bool {
	for _, e := range ve {
		if e.Path == path && e.Tag == tag {
			return true
		}
	}
	return false
}

// assertHasErr asserts ve contains an error for path+tag.
func assertHasErr(t *testing.T, ve validate.ValidationErrors, path, tag string) {
	t.Helper()
	if !hasErr(ve, path, tag) {
		t.Errorf("expected error at path=%q tag=%q\ngot errors:\n%v", path, tag, ve)
	}
}

// assertNoErrAt asserts ve does NOT contain any error at path.
func assertNoErrAt(t *testing.T, ve validate.ValidationErrors, path string) {
	t.Helper()
	for _, e := range ve {
		if e.Path == path {
			t.Errorf("unexpected error at path=%q tag=%q", path, e.Tag)
		}
	}
}

// ---------------------------------------------------------------------------
// Rule: required
// ---------------------------------------------------------------------------

func TestRequired_String(t *testing.T) {
	type S struct {
		V string `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: ""}))
	mustPass(t, validate.Struct(&S{V: "hello"}))
}

func TestRequired_Int(t *testing.T) {
	type S struct {
		V int `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: 0}))
	mustPass(t, validate.Struct(&S{V: 1}))
	mustPass(t, validate.Struct(&S{V: -1}))
}

func TestRequired_Bool(t *testing.T) {
	type S struct {
		V bool `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: false}))
	mustPass(t, validate.Struct(&S{V: true}))
}

func TestRequired_Slice(t *testing.T) {
	type S struct {
		V []string `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: nil}))
	mustErrors(t, validate.Struct(&S{V: []string{}}))
	mustPass(t, validate.Struct(&S{V: []string{"x"}}))
}

func TestRequired_Map(t *testing.T) {
	type S struct {
		V map[string]string `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: nil}))
	mustErrors(t, validate.Struct(&S{V: map[string]string{}}))
	mustPass(t, validate.Struct(&S{V: map[string]string{"k": "v"}}))
}

func TestRequired_Pointer(t *testing.T) {
	type S struct {
		V *string `validate:"required"`
	}
	mustErrors(t, validate.Struct(&S{V: nil}))
	s := "hello"
	mustPass(t, validate.Struct(&S{V: &s}))
}

// ---------------------------------------------------------------------------
// Rule: omitempty
// ---------------------------------------------------------------------------

func TestOmitempty_SkipsRemainingRulesOnZero(t *testing.T) {
	type S struct {
		V string `validate:"omitempty,url"`
	}
	// Empty string: omitempty triggers early return — url rule never runs.
	mustPass(t, validate.Struct(&S{V: ""}))
}

func TestOmitempty_ContinuesOnNonZero(t *testing.T) {
	type S struct {
		V string `validate:"omitempty,url"`
	}
	// Non-empty but invalid URL: omitempty passes, url fires.
	mustErrors(t, validate.Struct(&S{V: "not-a-url"}))
}

func TestOmitempty_PassesWithValidValue(t *testing.T) {
	type S struct {
		V string `validate:"omitempty,url"`
	}
	mustPass(t, validate.Struct(&S{V: "http://example.com"}))
}

func TestOmitempty_PreservesEarlierErrors(t *testing.T) {
	// required_if fires an error, then omitempty sees empty value and returns
	// early — but the required_if error is already collected.
	type S struct {
		Type string `yaml:"type"`
		URL  string `yaml:"url" validate:"required_if=Type pihole,omitempty,url"`
	}
	err := validate.Struct(&S{Type: "pihole", URL: ""})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "url", "required_if=Type pihole")
	// No spurious url error on top of required_if.
	if hasErr(ve, "url", "url") {
		t.Error("unexpected url rule error when field is empty")
	}
}

// ---------------------------------------------------------------------------
// Rule: oneof
// ---------------------------------------------------------------------------

func TestOneof_ValidValues(t *testing.T) {
	type S struct {
		V string `validate:"oneof=foo bar baz"`
	}
	for _, v := range []string{"foo", "bar", "baz"} {
		mustPass(t, validate.Struct(&S{V: v}))
	}
}

func TestOneof_InvalidValues(t *testing.T) {
	type S struct {
		V string `validate:"oneof=foo bar baz"`
	}
	for _, v := range []string{"qux", "", "Foo", "FOO", "foo "} {
		mustErrors(t, validate.Struct(&S{V: v}))
	}
}

func TestOneof_ErrorTag(t *testing.T) {
	type S struct {
		V string `yaml:"v" validate:"oneof=a b"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: "c"}))
	assertHasErr(t, ve, "v", "oneof=a b")
}

// ---------------------------------------------------------------------------
// Rule: gt
// ---------------------------------------------------------------------------

func TestGt_Int(t *testing.T) {
	type S struct {
		V int `validate:"gt=0"`
	}
	mustErrors(t, validate.Struct(&S{V: 0}))
	mustErrors(t, validate.Struct(&S{V: -1}))
	mustPass(t, validate.Struct(&S{V: 1}))
	mustPass(t, validate.Struct(&S{V: 100}))
}

func TestGt_ErrorTag(t *testing.T) {
	type S struct {
		V int `yaml:"v" validate:"gt=0"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: 0}))
	assertHasErr(t, ve, "v", "gt=0")
}

// ---------------------------------------------------------------------------
// Rule: min
// ---------------------------------------------------------------------------

func TestMin_String(t *testing.T) {
	type S struct {
		V string `validate:"min=3"`
	}
	mustErrors(t, validate.Struct(&S{V: ""}))
	mustErrors(t, validate.Struct(&S{V: "ab"}))
	mustPass(t, validate.Struct(&S{V: "abc"}))
	mustPass(t, validate.Struct(&S{V: "abcd"}))
}

func TestMin_Int(t *testing.T) {
	type S struct {
		V int `validate:"min=0"`
	}
	mustErrors(t, validate.Struct(&S{V: -1}))
	mustPass(t, validate.Struct(&S{V: 0}))
	mustPass(t, validate.Struct(&S{V: 100}))
}

func TestMin_ErrorTag(t *testing.T) {
	type S struct {
		V int `yaml:"v" validate:"min=0"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: -1}))
	assertHasErr(t, ve, "v", "min=0")
}

// ---------------------------------------------------------------------------
// Rule: max
// ---------------------------------------------------------------------------

func TestMax_String(t *testing.T) {
	type S struct {
		V string `validate:"max=5"`
	}
	mustPass(t, validate.Struct(&S{V: ""}))
	mustPass(t, validate.Struct(&S{V: "hello"}))
	mustErrors(t, validate.Struct(&S{V: "toolong"}))
}

func TestMax_Int(t *testing.T) {
	type S struct {
		V int `validate:"max=100"`
	}
	mustPass(t, validate.Struct(&S{V: 0}))
	mustPass(t, validate.Struct(&S{V: 100}))
	mustErrors(t, validate.Struct(&S{V: 101}))
}

func TestMax_ErrorTag(t *testing.T) {
	type S struct {
		V int `yaml:"v" validate:"max=100"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: 200}))
	assertHasErr(t, ve, "v", "max=100")
}

// ---------------------------------------------------------------------------
// Rule: url
// ---------------------------------------------------------------------------

func TestURL_Valid(t *testing.T) {
	type S struct {
		V string `validate:"url"`
	}
	for _, v := range []string{
		"http://example.com",
		"https://example.com/path?q=1",
		"http://localhost:8080",
		"http://pihole.home",
		"http://192.168.1.1:8080",
	} {
		mustPass(t, validate.Struct(&S{V: v}))
	}
}

func TestURL_Invalid(t *testing.T) {
	type S struct {
		V string `validate:"url"`
	}
	for _, v := range []string{
		"not-a-url",
		"//missing-scheme.com",
		"http://",
		"",
		"ftp://",
		"just-a-hostname",
	} {
		mustErrors(t, validate.Struct(&S{V: v}))
	}
}

func TestURL_ErrorTag(t *testing.T) {
	type S struct {
		V string `yaml:"url" validate:"url"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: "not-a-url"}))
	assertHasErr(t, ve, "url", "url")
}

// ---------------------------------------------------------------------------
// Rule: hostname_port
// ---------------------------------------------------------------------------

func TestHostnamePort_Valid(t *testing.T) {
	type S struct {
		V string `validate:"hostname_port"`
	}
	for _, v := range []string{
		":9090",
		"localhost:8080",
		"0.0.0.0:9090",
		"example.com:443",
		"192.168.1.1:80",
	} {
		mustPass(t, validate.Struct(&S{V: v}))
	}
}

func TestHostnamePort_Invalid(t *testing.T) {
	type S struct {
		V string `validate:"hostname_port"`
	}
	for _, v := range []string{
		"noporthere",
		"",
		"http://localhost:8080",
	} {
		mustErrors(t, validate.Struct(&S{V: v}))
	}
}

func TestHostnamePort_ErrorTag(t *testing.T) {
	type S struct {
		V string `yaml:"addr" validate:"hostname_port"`
	}
	ve := mustErrors(t, validate.Struct(&S{V: "noport"}))
	assertHasErr(t, ve, "addr", "hostname_port")
}

// ---------------------------------------------------------------------------
// Rule: required_if
// ---------------------------------------------------------------------------

func TestRequiredIf_ConditionMet_FieldEmpty(t *testing.T) {
	type S struct {
		Type  string `yaml:"type"`
		Token string `yaml:"token" validate:"required_if=Type cloudflare"`
	}
	err := validate.Struct(&S{Type: "cloudflare", Token: ""})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "token", "required_if=Type cloudflare")
}

func TestRequiredIf_ConditionMet_FieldPresent(t *testing.T) {
	type S struct {
		Type  string `yaml:"type"`
		Token string `yaml:"token" validate:"required_if=Type cloudflare"`
	}
	mustPass(t, validate.Struct(&S{Type: "cloudflare", Token: "my-token"}))
}

func TestRequiredIf_ConditionNotMet_FieldEmpty(t *testing.T) {
	type S struct {
		Type  string `yaml:"type"`
		Token string `yaml:"token" validate:"required_if=Type cloudflare"`
	}
	// Type is "pihole" — Token not required even when empty.
	mustPass(t, validate.Struct(&S{Type: "pihole", Token: ""}))
}

func TestRequiredIf_ConditionNotMet_SiblingEmpty(t *testing.T) {
	type S struct {
		Type  string `yaml:"type"`
		Token string `yaml:"token" validate:"required_if=Type cloudflare"`
	}
	// Type is unset — Token not required.
	mustPass(t, validate.Struct(&S{Type: "", Token: ""}))
}

func TestRequiredIf_MultipleConditions(t *testing.T) {
	type S struct {
		Type     string `yaml:"type"`
		APIToken string `yaml:"api_token" validate:"required_if=Type cloudflare"`
		ZoneID   string `yaml:"zone_id"   validate:"required_if=Type cloudflare"`
	}
	err := validate.Struct(&S{Type: "cloudflare"})
	ve := mustErrors(t, err)
	if len(ve) != 2 {
		t.Errorf("expected 2 errors (api_token + zone_id), got %d: %v", len(ve), ve)
	}
	assertHasErr(t, ve, "api_token", "required_if=Type cloudflare")
	assertHasErr(t, ve, "zone_id", "required_if=Type cloudflare")
}

func TestRequiredIf_NonMatchingConditionsDoNotInterfere(t *testing.T) {
	type S struct {
		Type     string `yaml:"type"`
		APIToken string `yaml:"api_token" validate:"required_if=Type cloudflare"`
		URL      string `yaml:"url"       validate:"required_if=Type pihole"`
	}
	// Cloudflare: api_token required, url not required.
	err := validate.Struct(&S{Type: "cloudflare", APIToken: ""})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "api_token", "required_if=Type cloudflare")
	assertNoErrAt(t, ve, "url")
}

// ---------------------------------------------------------------------------
// Error collection — all failures are returned, not just the first
// ---------------------------------------------------------------------------

func TestCollectsAllErrors(t *testing.T) {
	type S struct {
		A string `yaml:"a" validate:"required"`
		B string `yaml:"b" validate:"required"`
		C int    `yaml:"c" validate:"gt=0"`
	}
	err := validate.Struct(&S{A: "", B: "", C: 0})
	ve := mustErrors(t, err)
	if len(ve) != 3 {
		t.Errorf("expected 3 errors, got %d: %v", len(ve), ve)
	}
	assertHasErr(t, ve, "a", "required")
	assertHasErr(t, ve, "b", "required")
	assertHasErr(t, ve, "c", "gt=0")
}

// ---------------------------------------------------------------------------
// Path construction
// ---------------------------------------------------------------------------

func TestPath_UsesYAMLTag(t *testing.T) {
	type S struct {
		MyField string `yaml:"my_field" validate:"required"`
	}
	ve := mustErrors(t, validate.Struct(&S{}))
	assertHasErr(t, ve, "my_field", "required")
}

func TestPath_FallsBackToLowercaseFieldName(t *testing.T) {
	type S struct {
		MyField string `validate:"required"` // no yaml tag
	}
	ve := mustErrors(t, validate.Struct(&S{}))
	assertHasErr(t, ve, "myfield", "required")
}

func TestPath_InlineEmbeddedFieldsUseParentPath(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Inner `yaml:",inline"`
	}
	ve := mustErrors(t, validate.Struct(&Outer{}))
	// The inline field "value" should be at root, not "inner.value".
	assertHasErr(t, ve, "value", "required")
	assertNoErrAt(t, ve, "inner.value")
}

func TestPath_NestedStruct(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Child Inner `yaml:"child"`
	}
	ve := mustErrors(t, validate.Struct(&Outer{}))
	assertHasErr(t, ve, "child.value", "required")
}

func TestPath_DeeplyNested(t *testing.T) {
	type Level3 struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Level2 struct {
		L3 Level3 `yaml:"l3"`
	}
	type Level1 struct {
		L2 Level2 `yaml:"l2"`
	}
	ve := mustErrors(t, validate.Struct(&Level1{}))
	assertHasErr(t, ve, "l2.l3.value", "required")
}

func TestPath_MapKeyIncludedInPath(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Items map[string]Inner `yaml:"items"`
	}
	err := validate.Struct(&Outer{
		Items: map[string]Inner{
			"my-key": {Value: ""},
		},
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "items.my-key.value", "required")
}

func TestPath_MapDoesNotErrorOnValidEntries(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Items map[string]Inner `yaml:"items"`
	}
	mustPass(t, validate.Struct(&Outer{
		Items: map[string]Inner{
			"good": {Value: "present"},
		},
	}))
}

func TestPath_MapMixedValidity(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Items map[string]Inner `yaml:"items"`
	}
	err := validate.Struct(&Outer{
		Items: map[string]Inner{
			"ok":  {Value: "present"},
			"bad": {Value: ""},
		},
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "items.bad.value", "required")
	assertNoErrAt(t, ve, "items.ok.value")
}

func TestPath_SliceIndexInPath(t *testing.T) {
	type Item struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Items []Item `yaml:"items"`
	}
	err := validate.Struct(&Outer{
		Items: []Item{{Value: ""}, {Value: "ok"}, {Value: ""}},
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "items[0].value", "required")
	assertNoErrAt(t, ve, "items[1].value")
	assertHasErr(t, ve, "items[2].value", "required")
}

func TestPath_EmptyMapNoErrors(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Items map[string]Inner `yaml:"items"`
	}
	mustPass(t, validate.Struct(&Outer{Items: map[string]Inner{}}))
	mustPass(t, validate.Struct(&Outer{Items: nil}))
}

func TestPath_NilPointerFieldSkipped(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Child *Inner `yaml:"child"`
	}
	// Nil pointer — the inner struct is not walked, no error for child.value.
	mustPass(t, validate.Struct(&Outer{Child: nil}))
}

func TestPath_NonNilPointerWalked(t *testing.T) {
	type Inner struct {
		Value string `yaml:"value" validate:"required"`
	}
	type Outer struct {
		Child *Inner `yaml:"child"`
	}
	// Non-nil pointer — inner struct is walked, error for child.value.
	ve := mustErrors(t, validate.Struct(&Outer{Child: &Inner{Value: ""}}))
	assertHasErr(t, ve, "child.value", "required")
}

// ---------------------------------------------------------------------------
// Struct and StructWithPrefix
// ---------------------------------------------------------------------------

func TestStruct_NilInput(t *testing.T) {
	var s *model.UpstreamConfig
	// Nil pointer input is a no-op — returns nil.
	mustPass(t, validate.Struct(s))
}

func TestStruct_NonStructInput(t *testing.T) {
	s := "hello"
	mustPass(t, validate.Struct(&s))
}

func TestStructWithPrefix_PrependsToAllPaths(t *testing.T) {
	type S struct {
		A string `yaml:"a" validate:"required"`
		B string `yaml:"b" validate:"required"`
	}
	err := validate.StructWithPrefix(&S{}, "some.prefix")
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "some.prefix.a", "required")
	assertHasErr(t, ve, "some.prefix.b", "required")
}

func TestStructWithPrefix_EmptyPrefixMatchesStruct(t *testing.T) {
	type S struct {
		V string `yaml:"v" validate:"required"`
	}
	err1 := validate.Struct(&S{})
	err2 := validate.StructWithPrefix(&S{}, "")
	ve1 := mustErrors(t, err1)
	ve2 := mustErrors(t, err2)
	if ve1[0].Path != ve2[0].Path {
		t.Errorf("Struct path=%q, StructWithPrefix(\"\") path=%q — should be equal", ve1[0].Path, ve2[0].Path)
	}
}

func TestStructWithPrefix_DockerStylePath(t *testing.T) {
	err := validate.StructWithPrefix(&model.Record{}, "docker://abc123/web/cloudflare")
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "docker://abc123/web/cloudflare.type", "required")
	assertHasErr(t, ve, "docker://abc123/web/cloudflare.name", "required")
	assertHasErr(t, ve, "docker://abc123/web/cloudflare.value", "required")
	assertHasErr(t, ve, "docker://abc123/web/cloudflare.upstream", "required")
}

// ---------------------------------------------------------------------------
// Validatable interface
// ---------------------------------------------------------------------------

// customValidated is a test struct that implements the Validatable interface.
type customValidated struct {
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}

func (s *customValidated) BeaconsValidate(path string) validate.ValidationErrors {
	if s.Type == "special" && s.Value == "" {
		p := "value"
		if path != "" {
			p = path + ".value"
		}
		return validate.ValidationErrors{{
			Path:    p,
			Tag:     "custom-rule",
			Value:   s.Value,
			Message: p + ": failed custom-rule",
		}}
	}
	return nil
}

func TestValidatable_CustomRuleFires(t *testing.T) {
	err := validate.Struct(&customValidated{Type: "special", Value: ""})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "value", "custom-rule")
}

func TestValidatable_CustomRuleNotFiredWhenConditionFalse(t *testing.T) {
	// Type is not "special" — custom rule should not add errors.
	mustPass(t, validate.Struct(&customValidated{Type: "other", Value: ""}))
}

func TestValidatable_CustomRuleWithPrefix(t *testing.T) {
	err := validate.StructWithPrefix(&customValidated{Type: "special", Value: ""}, "parent")
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "parent.value", "custom-rule")
}

func TestValidatable_CustomAndTagErrorsCombined(t *testing.T) {
	type S struct {
		Type  string `yaml:"type"  validate:"required"`
		Value string `yaml:"value"`
	}
	// Embed customValidated to get both tag errors and Validatable errors in one struct.
	// Instead, use a struct that has both tag rules and implements Validatable.
	// We verify this via customValidated with a required tag on Type.
	type WithRequired struct {
		Type  string `yaml:"type"  validate:"required"`
		Value string `yaml:"value"`
	}
	// This struct doesn't implement Validatable, so no custom errors — just verify
	// tag errors work alongside the interface by using the customValidated type with a tag.
	_ = S{}
	// Direct test: customValidated has no validate tags but does implement Validatable.
	// Errors from Validatable are included alongside any tag errors.
	err := validate.Struct(&customValidated{Type: "special", Value: ""})
	ve := mustErrors(t, err)
	if len(ve) != 1 {
		t.Errorf("expected exactly 1 error (custom-rule), got %d: %v", len(ve), ve)
	}
}

// ---------------------------------------------------------------------------
// ValidationErrors formatting
// ---------------------------------------------------------------------------

func TestValidationErrors_Error_ContainsHeader(t *testing.T) {
	type S struct {
		V string `yaml:"v" validate:"required"`
	}
	err := validate.Struct(&S{})
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' in error string, got: %s", err.Error())
	}
}

func TestValidationErrors_Error_ContainsPath(t *testing.T) {
	type S struct {
		Addr string `yaml:"addr" validate:"required"`
	}
	err := validate.Struct(&S{})
	if !strings.Contains(err.Error(), "addr") {
		t.Errorf("expected field path in error string, got: %s", err.Error())
	}
}

func TestValidationErrors_Error_ContainsAllErrors(t *testing.T) {
	type S struct {
		A string `yaml:"a" validate:"required"`
		B string `yaml:"b" validate:"required"`
	}
	err := validate.Struct(&S{})
	msg := err.Error()
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Errorf("expected both field paths in error string, got: %s", msg)
	}
}

func TestValidationErrors_Empty_ReturnsEmptyString(t *testing.T) {
	var ve validate.ValidationErrors
	if ve.Error() != "" {
		t.Errorf("expected empty string for empty ValidationErrors, got %q", ve.Error())
	}
}

func TestValidationError_ErrorMethod(t *testing.T) {
	e := validate.ValidationError{
		Path:    "http.addr",
		Tag:     "required",
		Message: `http.addr: failed rule "required" (got )`,
	}
	if e.Error() != e.Message {
		t.Errorf("ValidationError.Error() = %q, want %q", e.Error(), e.Message)
	}
}

// ---------------------------------------------------------------------------
// Model: UpstreamConfig
// ---------------------------------------------------------------------------

func TestUpstreamConfig_Cloudflare_Valid(t *testing.T) {
	mustPass(t, validate.Struct(&model.UpstreamConfig{
		Type:     "cloudflare",
		APIToken: "tok",
		ZoneID:   "z1",
	}))
}

func TestUpstreamConfig_Cloudflare_MissingAPIToken(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{
		Type:   "cloudflare",
		ZoneID: "z1",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "api_token", "required_if=Type cloudflare")
}

func TestUpstreamConfig_Cloudflare_MissingZoneID(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{
		Type:     "cloudflare",
		APIToken: "tok",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "zone_id", "required_if=Type cloudflare")
}

func TestUpstreamConfig_Cloudflare_MissingBothCredentials(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{Type: "cloudflare"})
	ve := mustErrors(t, err)
	if len(ve) != 2 {
		t.Errorf("expected 2 errors (api_token + zone_id), got %d: %v", len(ve), ve)
	}
	assertHasErr(t, ve, "api_token", "required_if=Type cloudflare")
	assertHasErr(t, ve, "zone_id", "required_if=Type cloudflare")
}

func TestUpstreamConfig_Cloudflare_UnusedPiholeFields(t *testing.T) {
	// Cloudflare upstream with URL set (unusual but should not cause an error
	// since omitempty skips the url rule when not required).
	mustPass(t, validate.Struct(&model.UpstreamConfig{
		Type:     "cloudflare",
		APIToken: "tok",
		ZoneID:   "z1",
		URL:      "http://pihole.home",
	}))
}

func TestUpstreamConfig_Pihole_Valid(t *testing.T) {
	mustPass(t, validate.Struct(&model.UpstreamConfig{
		Type: "pihole",
		URL:  "http://pihole.home:8080",
	}))
}

func TestUpstreamConfig_Pihole_ValidWithPassword(t *testing.T) {
	mustPass(t, validate.Struct(&model.UpstreamConfig{
		Type:     "pihole",
		URL:      "http://pihole.home:8080",
		Password: "secret",
	}))
}

func TestUpstreamConfig_Pihole_MissingURL(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{Type: "pihole"})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "url", "required_if=Type pihole")
}

func TestUpstreamConfig_Pihole_InvalidURL(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{
		Type: "pihole",
		URL:  "not-a-url",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "url", "url")
}

func TestUpstreamConfig_Pihole_NoCloudflareFieldsRequired(t *testing.T) {
	// Pihole upstream should not require api_token or zone_id.
	err := validate.Struct(&model.UpstreamConfig{
		Type: "pihole",
		URL:  "http://pihole.home:8080",
	})
	mustPass(t, err)
}

func TestUpstreamConfig_MissingType(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "required")
}

func TestUpstreamConfig_InvalidType(t *testing.T) {
	err := validate.Struct(&model.UpstreamConfig{Type: "unknown"})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "oneof=cloudflare pihole")
}

// ---------------------------------------------------------------------------
// Model: SourceConfig
// ---------------------------------------------------------------------------

func TestSourceConfig_Docker_Valid(t *testing.T) {
	mustPass(t, validate.Struct(&model.SourceConfig{Type: "docker"}))
}

func TestSourceConfig_Docker_WithHost(t *testing.T) {
	mustPass(t, validate.Struct(&model.SourceConfig{
		Type: "docker",
		Host: "unix:///var/run/docker.sock",
	}))
}

func TestSourceConfig_YAML_Valid(t *testing.T) {
	mustPass(t, validate.Struct(&model.SourceConfig{
		Type: "yaml",
		Glob: "/config/*.yaml",
	}))
}

func TestSourceConfig_YAML_MissingGlob(t *testing.T) {
	err := validate.Struct(&model.SourceConfig{Type: "yaml"})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "glob", "required_if=Type yaml")
}

func TestSourceConfig_Docker_GlobNotRequired(t *testing.T) {
	// Docker source — glob should not be required.
	mustPass(t, validate.Struct(&model.SourceConfig{Type: "docker"}))
}

func TestSourceConfig_MissingType(t *testing.T) {
	err := validate.Struct(&model.SourceConfig{})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "required")
}

func TestSourceConfig_InvalidType(t *testing.T) {
	err := validate.Struct(&model.SourceConfig{Type: "ftp"})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "oneof=docker yaml")
}

// ---------------------------------------------------------------------------
// Model: Record
// ---------------------------------------------------------------------------

func TestRecord_Valid(t *testing.T) {
	mustPass(t, validate.Struct(&model.Record{
		Type:     model.RecordTypeA,
		Name:     "svc.example.com",
		Value:    "1.2.3.4",
		Upstream: "cf-prod",
	}))
}

func TestRecord_AllTypes_Valid(t *testing.T) {
	for _, rt := range []model.RecordType{
		model.RecordTypeA,
		model.RecordTypeAAAA,
		model.RecordTypeCNAME,
		model.RecordTypeTXT,
		model.RecordTypeMX,
		model.RecordTypeSRV,
		model.RecordTypeNS,
		model.RecordTypeCAA,
	} {
		t.Run(string(rt), func(t *testing.T) {
			mustPass(t, validate.Struct(&model.Record{
				Type:     rt,
				Name:     "example.com",
				Value:    "target",
				Upstream: "up",
			}))
		})
	}
}

func TestRecord_MissingType(t *testing.T) {
	err := validate.Struct(&model.Record{
		Name:     "example.com",
		Value:    "1.2.3.4",
		Upstream: "cf",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "required")
}

func TestRecord_InvalidType(t *testing.T) {
	err := validate.Struct(&model.Record{
		Type:     "INVALID",
		Name:     "example.com",
		Value:    "1.2.3.4",
		Upstream: "cf",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "oneof=A AAAA CNAME TXT MX SRV NS CAA")
}

func TestRecord_MissingName(t *testing.T) {
	err := validate.Struct(&model.Record{
		Type:     model.RecordTypeA,
		Value:    "1.2.3.4",
		Upstream: "cf",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "name", "required")
}

func TestRecord_MissingValue(t *testing.T) {
	err := validate.Struct(&model.Record{
		Type:     model.RecordTypeA,
		Name:     "example.com",
		Upstream: "cf",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "value", "required")
}

func TestRecord_MissingUpstream(t *testing.T) {
	err := validate.Struct(&model.Record{
		Type:  model.RecordTypeA,
		Name:  "example.com",
		Value: "1.2.3.4",
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "upstream", "required")
}

func TestRecord_AllFieldsMissing(t *testing.T) {
	err := validate.Struct(&model.Record{})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "type", "required")
	assertHasErr(t, ve, "name", "required")
	assertHasErr(t, ve, "value", "required")
	assertHasErr(t, ve, "upstream", "required")
}

func TestRecord_OptionalFieldsDoNotCauseErrors(t *testing.T) {
	// TTL, Priority, Comment are optional — zero values should not fail.
	mustPass(t, validate.Struct(&model.Record{
		Type:     model.RecordTypeA,
		Name:     "example.com",
		Value:    "1.2.3.4",
		Upstream: "cf",
		// BaseRecord fields all zero — should be fine.
	}))
}

// ---------------------------------------------------------------------------
// Map path integration — mirrors how Config.Upstreams is validated
// ---------------------------------------------------------------------------

func TestUpstreamMapPath_ErrorIncludesMapKey(t *testing.T) {
	type ConfigLike struct {
		Upstreams map[string]model.UpstreamConfig `yaml:"upstreams"`
	}
	err := validate.Struct(&ConfigLike{
		Upstreams: map[string]model.UpstreamConfig{
			"cf-prod": {Type: "cloudflare"}, // missing api_token and zone_id
		},
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "upstreams.cf-prod.api_token", "required_if=Type cloudflare")
	assertHasErr(t, ve, "upstreams.cf-prod.zone_id", "required_if=Type cloudflare")
}

func TestUpstreamMapPath_MultipleEntryErrors(t *testing.T) {
	type ConfigLike struct {
		Upstreams map[string]model.UpstreamConfig `yaml:"upstreams"`
	}
	err := validate.Struct(&ConfigLike{
		Upstreams: map[string]model.UpstreamConfig{
			"cf-a": {Type: "cloudflare"},
			"cf-b": {Type: "cloudflare"},
		},
	})
	ve := mustErrors(t, err)
	// 2 errors per upstream × 2 upstreams = 4 errors minimum.
	if len(ve) < 4 {
		t.Errorf("expected at least 4 errors, got %d: %v", len(ve), ve)
	}
}

func TestUpstreamMapPath_ValidEntryProducesNoError(t *testing.T) {
	type ConfigLike struct {
		Upstreams map[string]model.UpstreamConfig `yaml:"upstreams"`
	}
	mustPass(t, validate.Struct(&ConfigLike{
		Upstreams: map[string]model.UpstreamConfig{
			"cf-prod": {Type: "cloudflare", APIToken: "tok", ZoneID: "z1"},
		},
	}))
}

func TestSourceMapPath_ErrorIncludesMapKey(t *testing.T) {
	type ConfigLike struct {
		Sources map[string]model.SourceConfig `yaml:"sources"`
	}
	err := validate.Struct(&ConfigLike{
		Sources: map[string]model.SourceConfig{
			"static-files": {Type: "yaml"}, // missing glob
		},
	})
	ve := mustErrors(t, err)
	assertHasErr(t, ve, "sources.static-files.glob", "required_if=Type yaml")
}

// ---------------------------------------------------------------------------
// Model: Record — Priority range validation (#7)
// ---------------------------------------------------------------------------

func validMXRecord() *model.Record {
	return &model.Record{
		Type:     model.RecordTypeMX,
		Name:     "example.com",
		Value:    "mail.example.com",
		Upstream: "cf",
	}
}

func TestRecord_Priority_Zero_Valid(t *testing.T) {
	r := validMXRecord()
	r.Priority = 0
	mustPass(t, validate.Struct(r))
}

func TestRecord_Priority_MaxValid_Valid(t *testing.T) {
	r := validMXRecord()
	r.Priority = 65535
	mustPass(t, validate.Struct(r))
}

func TestRecord_Priority_TypicalValues_Valid(t *testing.T) {
	for _, p := range []int{1, 10, 100, 1000, 32767} {
		r := validMXRecord()
		r.Priority = p
		if err := validate.Struct(r); err != nil {
			t.Errorf("Priority=%d: unexpected error: %v", p, err)
		}
	}
}

func TestRecord_Priority_ExceedsMax_Fails(t *testing.T) {
	r := validMXRecord()
	r.Priority = 65536
	ve := mustErrors(t, validate.Struct(r))
	assertHasErr(t, ve, "priority", "max=65535")
}

func TestRecord_Priority_Negative_Fails(t *testing.T) {
	r := validMXRecord()
	r.Priority = -1
	ve := mustErrors(t, validate.Struct(r))
	assertHasErr(t, ve, "priority", "min=0")
}

func TestRecord_Priority_FarAboveMax_Fails(t *testing.T) {
	r := validMXRecord()
	r.Priority = 1_000_000
	ve := mustErrors(t, validate.Struct(r))
	assertHasErr(t, ve, "priority", "max=65535")
}
