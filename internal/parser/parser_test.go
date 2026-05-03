package parser

import (
	"os"
	"testing"

	"github.com/bholten/idlc-go/internal/corpus"
)

func TestParseChatMessage(t *testing.T) {
	corpus.RequireOrSkip(t)
	src, err := os.ReadFile("../../testdata/idl/ChatMessage.idl")

	if err != nil {
		t.Fatal(err)
	}

	f, err := Parse("ChatMessage.idl", src)

	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if f.Package != "server.chat" {
		t.Errorf("Package = %q, want server.chat", f.Package)
	}

	if len(f.Imports) != 1 || f.Imports[0].Path != "engine.core.ManagedObject" {
		t.Errorf("Imports = %+v, want one engine.core.ManagedObject", f.Imports)
	}

	if f.Class == nil {
		t.Fatal("Class is nil")
	}

	c := f.Class

	if c.Name != "ChatMessage" {
		t.Errorf("Class.Name = %q, want ChatMessage", c.Name)
	}

	if c.Base != "ManagedObject" {
		t.Errorf("Class.Base = %q, want ManagedObject", c.Base)
	}

	if len(c.Annotations) != 1 || c.Annotations[0].Name != "json" {
		t.Errorf("class annotations = %+v, want one @json", c.Annotations)
	}

	if len(c.Members) != 4 {
		t.Fatalf("len(Members) = %d, want 4", len(c.Members))
	}

	// Field
	field, ok := c.Members[0].(*Field)

	if !ok {
		t.Fatalf("Members[0] is %T, want *Field", c.Members[0])
	}

	if field.Name != "message" || field.Type.Name != "string" || field.Visibility != Protected {
		t.Errorf("field = %+v", field)
	}

	// Constructor
	ctor, ok := c.Members[1].(*Constructor)

	if !ok {
		t.Fatalf("Members[1] is %T, want *Constructor", c.Members[1])
	}

	if ctor.Name != "ChatMessage" || ctor.Visibility != Public {
		t.Errorf("ctor = %+v", ctor)
	}

	if len(ctor.Params) != 0 {
		t.Errorf("ctor has %d params, want 0", len(ctor.Params))
	}

	// setString method
	setString, ok := c.Members[2].(*Method)

	if !ok {
		t.Fatalf("Members[2] is %T, want *Method", c.Members[2])
	}

	if setString.Name != "setString" || setString.Return.Name != "void" {
		t.Errorf("setString = %+v", setString)
	}

	if len(setString.Params) != 1 {
		t.Fatalf("setString has %d params, want 1", len(setString.Params))
	}

	p := setString.Params[0]

	if !p.Final || p.Type.Name != "string" || p.Name != "msg" {
		t.Errorf("setString param = %+v", p)
	}

	// toString method with @read
	toString, ok := c.Members[3].(*Method)

	if !ok {
		t.Fatalf("Members[3] is %T, want *Method", c.Members[3])
	}

	if toString.Name != "toString" || toString.Return.Name != "string" {
		t.Errorf("toString = %+v", toString)
	}

	if len(toString.Annotations) != 1 || toString.Annotations[0].Name != "read" {
		t.Errorf("toString annotations = %+v, want one @read", toString.Annotations)
	}
}
