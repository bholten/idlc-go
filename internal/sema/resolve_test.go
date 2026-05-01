package sema

import (
	"os"
	"testing"

	"github.com/bholten/tools/idlc-go/internal/corpus"
	"github.com/bholten/tools/idlc-go/internal/parser"
)

func TestResolveChatMessage(t *testing.T) {
	corpus.RequireOrSkip(t)
	src, err := os.ReadFile("../../testdata/idl/ChatMessage.idl")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.Parse("ChatMessage.idl", src)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Resolve(f)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := m.Class.Name, "ChatMessage"; got != want {
		t.Errorf("Class.Name = %q, want %q", got, want)
	}
	if !m.Class.HasJSON {
		t.Error("expected HasJSON = true (from @json)")
	}
	if m.Class.Base != "ManagedObject" {
		t.Errorf("Class.Base = %q", m.Class.Base)
	}
	if m.Class.ImplName != "ChatMessageImplementation" {
		t.Errorf("ImplName = %q", m.Class.ImplName)
	}

	if len(m.Class.Fields) != 1 {
		t.Fatalf("len(Fields) = %d", len(m.Class.Fields))
	}
	field := m.Class.Fields[0]
	if CppRender(field.IDLType) != "String" || field.Hash != 0xbd8f57ac || field.HashInput != "ChatMessage.message" {
		t.Errorf("field = %+v (cpp=%q)", field, CppRender(field.IDLType))
	}

	if len(m.Class.Methods) != 2 {
		t.Fatalf("len(Methods) = %d", len(m.Class.Methods))
	}
	setString := m.Class.Methods[0]
	if setString.Name != "setString" || setString.Return.Name != "void" || !setString.IsVoid() {
		t.Errorf("setString = %+v", setString)
	}
	if setString.RPCName != "RPC_SETSTRING__STRING_" {
		t.Errorf("setString.RPCName = %q", setString.RPCName)
	}
	if setString.RPCSeed == nil || *setString.RPCSeed != 3293646548 {
		t.Errorf("setString.RPCSeed = %v, want 3293646548", setString.RPCSeed)
	}

	toString := m.Class.Methods[1]
	if !toString.IsRead {
		t.Error("toString should be IsRead (from @read)")
	}
	if toString.RPCName != "RPC_TOSTRING__" {
		t.Errorf("toString.RPCName = %q", toString.RPCName)
	}
	if toString.RPCSeed != nil {
		t.Errorf("toString.RPCSeed should be nil, got %v", *toString.RPCSeed)
	}
}
