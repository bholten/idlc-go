package cpp

import (
	"fmt"
	"io"

	"github.com/bholten/tools/idlc-go/internal/sema"
)

// emitHelperHeader writes the FooHelper class declaration.
func emitHelperHeader(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "class %s : public DistributedObjectClassHelper, public Singleton<%s> {\n", c.Helper, c.Helper)
	fmt.Fprintf(w, "\tstatic %s* staticInitializer;\n\n", c.Helper)
	fmt.Fprintf(w, "public:\n")
	fmt.Fprintf(w, "\t%s();\n\n", c.Helper)
	fmt.Fprintf(w, "\tvoid finalizeHelper();\n\n")
	fmt.Fprintf(w, "\tDistributedObject* instantiateObject();\n\n")
	fmt.Fprintf(w, "\tDistributedObjectPOD* instantiatePOD();\n\n")
	fmt.Fprintf(w, "\tDistributedObjectServant* instantiateServant();\n\n")
	fmt.Fprintf(w, "\tDistributedObjectAdapter* createAdapter(DistributedObjectStub* obj);\n\n")
	fmt.Fprintf(w, "\tfriend class Singleton<%s>;\n", c.Helper)
	fmt.Fprintf(w, "};\n\n")
}

// emitHelperSource writes the FooHelper .cpp definitions.
func emitHelperSource(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "/*\n")
	fmt.Fprintf(w, " *\t%s\n", c.Helper)
	fmt.Fprintf(w, " */\n\n")

	fmt.Fprintf(w, "%s* %s::staticInitializer = %s::instance();\n\n", c.Helper, c.Helper, c.Helper)

	fmt.Fprintf(w, "%s::%s() {\n", c.Helper, c.Helper)
	fmt.Fprintf(w, "\tclassName = \"%s\";\n\n", c.Name)
	fmt.Fprintf(w, "\tCore::getObjectBroker()->registerClass(className, this);\n")
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "void %s::finalizeHelper() {\n", c.Helper)
	fmt.Fprintf(w, "\t%s::finalize();\n", c.Helper)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "DistributedObject* %s::instantiateObject() {\n", c.Helper)
	fmt.Fprintf(w, "\treturn new %s(DummyConstructorParameter::instance());\n", c.Name)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "DistributedObjectServant* %s::instantiateServant() {\n", c.Helper)

	if customCtor(c) != nil || hasNoIDLCtor(c) {
		// Custom-args ctor: helper can't synthesise the user args.
		// No IDL ctor at all: the JAR omits the stub default ctor
		// entirely (see emitStubCtors / emitStubHeader), so the helper
		// has no `new Class()` to call. Both paths fall back to the
		// dummy ctor — same call shape as instantiateObject above.
		fmt.Fprintf(w, "\treturn new %s(DummyConstructorParameter::instance());\n", c.ImplName)
	} else {
		fmt.Fprintf(w, "\treturn new %s();\n", c.ImplName)
	}

	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "DistributedObjectPOD* %s::instantiatePOD() {\n", c.Helper)
	fmt.Fprintf(w, "\treturn new %s();\n", c.POD)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "DistributedObjectAdapter* %s::createAdapter(DistributedObjectStub* obj) {\n", c.Helper)
	fmt.Fprintf(w, "\tDistributedObjectAdapter* adapter = new %s(static_cast<%s*>(obj));\n\n", c.Adapter, c.Name)
	fmt.Fprintf(w, "\tobj->_setClassName(className);\n")
	fmt.Fprintf(w, "\tobj->_setClassHelper(this);\n\n")
	fmt.Fprintf(w, "\tadapter->setStub(obj);\n\n")
	fmt.Fprintf(w, "\treturn adapter;\n")
	fmt.Fprintf(w, "}\n\n")
}
