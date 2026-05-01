package lexer

import "testing"

func TestNextSimpleIDL(t *testing.T) {
	src := []byte(`
package server.chat;

import engine.core.ManagedObject;

@json
class ChatMessage extends ManagedObject {
	protected string message;
}
`)

	want := []struct {
		k   Kind
		lit string
	}{
		{KwPackage, "package"}, {Ident, "server"}, {Dot, "."}, {Ident, "chat"}, {Semi, ";"},
		{KwImport, "import"}, {Ident, "engine"}, {Dot, "."}, {Ident, "core"}, {Dot, "."}, {Ident, "ManagedObject"}, {Semi, ";"},
		{At, "@"}, {Ident, "json"},
		{KwClass, "class"}, {Ident, "ChatMessage"}, {KwExtends, "extends"}, {Ident, "ManagedObject"}, {LBrace, "{"},
		{KwProtected, "protected"}, {Ident, "string"}, {Ident, "message"}, {Semi, ";"},
		{RBrace, "}"},
		{EOF, ""},
	}

	l := New("t.idl", src)
	for i, w := range want {
		got := l.Next()
		if got.Kind != w.k || (w.lit != "" && got.Lit != w.lit) {
			t.Fatalf("token %d: got {%v %q}, want {%v %q}", i, got.Kind, got.Lit, w.k, w.lit)
		}
	}
}

func TestSkipsBlockAndLineComments(t *testing.T) {
	src := []byte(`/* header */
		// line
		package /* inline */ p; /* trailing */
		// end
	`)
	l := New("t.idl", src)
	tokens := []Kind{KwPackage, Ident, Semi, EOF}
	for i, want := range tokens {
		got := l.Next()
		if got.Kind != want {
			t.Fatalf("token %d: got %v (%q), want %v", i, got.Kind, got.Lit, want)
		}
	}
}

func TestCaptureBalancedBlockSimple(t *testing.T) {
	// The opener '{' has already been consumed by the caller before
	// CaptureBalancedBlock runs — that's how the parser uses it.
	src := []byte(`{ message = ""; }`)
	l := New("t.idl", src)
	if tok := l.Next(); tok.Kind != LBrace {
		t.Fatalf("expected '{', got %v", tok.Kind)
	}
	body, err := l.CaptureBalancedBlock()
	if err != nil {
		t.Fatal(err)
	}
	want := ` message = ""; `
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	if tok := l.Next(); tok.Kind != EOF {
		t.Fatalf("expected EOF, got %v", tok.Kind)
	}
}

func TestCaptureBalancedBlockNested(t *testing.T) {
	src := []byte(`{
	if (x) {
		return "{nested}";
	}
}`)
	l := New("t.idl", src)
	l.Next() // consume '{'
	body, err := l.CaptureBalancedBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(body, `return "{nested}";`) {
		t.Fatalf("body missing nested string: %q", body)
	}
	if !contains(body, "if (x) {") {
		t.Fatalf("body missing nested brace: %q", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
