package workflow

import "testing"

func TestReadEmbeddedSchema_KnownType(t *testing.T) {
	data, ok := ReadEmbeddedSchema("core.if")
	if !ok {
		t.Fatal("expected schema for core.if")
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty schema bytes")
	}
}

func TestReadEmbeddedSchema_UnknownType(t *testing.T) {
	if _, ok := ReadEmbeddedSchema("bogus.no_such_type"); ok {
		t.Fatal("expected no schema for bogus type")
	}
}

func TestReadEmbeddedSchema_BrowserFallback(t *testing.T) {
	// No dedicated linkedin.find_by_keyword.json exists; resolution must fall
	// back to action.find_by_keyword.json (same rules as LoadDefaultSchema).
	if _, ok := ReadEmbeddedSchema("linkedin.find_by_keyword"); !ok {
		t.Fatal("expected action-suffix fallback schema for linkedin.find_by_keyword")
	}
}

func TestSchemaTitle(t *testing.T) {
	// Bundled schemas carry no title/name fields today — the helper must
	// return "" rather than error or invent a value.
	if got := SchemaTitle("core.if"); got != "" {
		t.Fatalf("expected empty title for core.if, got %q", got)
	}
	if got := SchemaTitle("bogus.no_such_type"); got != "" {
		t.Fatalf("expected empty title for unknown type, got %q", got)
	}
}
