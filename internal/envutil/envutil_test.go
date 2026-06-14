package envutil_test

import (
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/envutil"
)

// ---------------------------------------------------------------------------
// Expand (strict)
// ---------------------------------------------------------------------------

func TestExpand_AllVarsSet(t *testing.T) {
	t.Setenv("ENVUTIL_TEST_A", "hello")
	t.Setenv("ENVUTIL_TEST_B", "world")
	got, err := envutil.Expand("${ENVUTIL_TEST_A} ${ENVUTIL_TEST_B}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestExpand_DollarSyntax(t *testing.T) {
	t.Setenv("ENVUTIL_TEST_VAL", "42")
	got, err := envutil.Expand("$ENVUTIL_TEST_VAL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestExpand_NoVars(t *testing.T) {
	got, err := envutil.Expand("no vars here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "no vars here" {
		t.Errorf("got %q, want %q", got, "no vars here")
	}
}

func TestExpand_EmptyString(t *testing.T) {
	got, err := envutil.Expand("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpand_MissingVar_ReturnsError(t *testing.T) {
	_, err := envutil.Expand("${ENVUTIL_TEST_DEFINITELY_MISSING_XYZ}")
	if err == nil {
		t.Fatal("expected error for missing var, got nil")
	}
	if !strings.Contains(err.Error(), "ENVUTIL_TEST_DEFINITELY_MISSING_XYZ") {
		t.Errorf("error should name missing var, got: %v", err)
	}
}

func TestExpand_MultipleMissingVars_AllReported(t *testing.T) {
	_, err := envutil.Expand("${ENVUTIL_MISSING_P} ${ENVUTIL_MISSING_Q}")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ENVUTIL_MISSING_P") {
		t.Errorf("error should mention ENVUTIL_MISSING_P, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ENVUTIL_MISSING_Q") {
		t.Errorf("error should mention ENVUTIL_MISSING_Q, got: %v", err)
	}
}

func TestExpand_MixedSetAndMissing_ErrorOnMissing(t *testing.T) {
	t.Setenv("ENVUTIL_TEST_SET", "value")
	_, err := envutil.Expand("${ENVUTIL_TEST_SET} ${ENVUTIL_TEST_NOT_SET_ZZZ}")
	if err == nil {
		t.Fatal("expected error for missing var")
	}
}

func TestExpand_VarInYAMLStyleContent(t *testing.T) {
	t.Setenv("ENVUTIL_TOKEN", "tok-abc")
	got, err := envutil.Expand("api_token: ${ENVUTIL_TOKEN}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "api_token: tok-abc" {
		t.Errorf("got %q, want %q", got, "api_token: tok-abc")
	}
}

// ---------------------------------------------------------------------------
// ExpandLenient
// ---------------------------------------------------------------------------

func TestExpandLenient_SetVarExpanded(t *testing.T) {
	t.Setenv("ENVUTIL_LENIENT_VAR", "expanded")
	got := envutil.ExpandLenient("${ENVUTIL_LENIENT_VAR}")
	if got != "expanded" {
		t.Errorf("got %q, want %q", got, "expanded")
	}
}

func TestExpandLenient_MissingVarBecomesEmpty(t *testing.T) {
	got := envutil.ExpandLenient("before ${ENVUTIL_LENIENT_MISSING_ABC} after")
	if got != "before  after" {
		t.Errorf("got %q, want %q", got, "before  after")
	}
}

func TestExpandLenient_AllMissingProducesSpaces(t *testing.T) {
	got := envutil.ExpandLenient("${ENVUTIL_MISSING_1} ${ENVUTIL_MISSING_2}")
	// Both missing → both replaced by empty string → " "
	if got != " " {
		t.Errorf("got %q, want %q", got, " ")
	}
}

func TestExpandLenient_EmptyString(t *testing.T) {
	got := envutil.ExpandLenient("")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExpandLenient_NoVars(t *testing.T) {
	got := envutil.ExpandLenient("static content")
	if got != "static content" {
		t.Errorf("got %q, want %q", got, "static content")
	}
}

func TestExpandLenient_MixedSetAndMissing(t *testing.T) {
	t.Setenv("ENVUTIL_LENIENT_SET", "hello")
	got := envutil.ExpandLenient("${ENVUTIL_LENIENT_SET} ${ENVUTIL_LENIENT_UNSET_ZZZ}")
	if got != "hello " {
		t.Errorf("got %q, want %q", got, "hello ")
	}
}
