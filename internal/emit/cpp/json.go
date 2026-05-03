package cpp

import (
	"fmt"
	"io"

	"github.com/bholten/idlc-go/internal/sema"
)

// emitImplWriteJSON writes FooImplementation::writeJSON, gated on @json.
// Root classes don't call a parent writeJSON and append a top-level
// `j["_className"] = _className;` line; non-root classes call parent
// first and only emit the per-class object.
//
// Empty-class quirk: when the class has no serializable fields the JAR
// declares the local `thisObject` but never assigns it to `j`. Faithfully
// reproduced — verified against engine3's Facade.cpp, where the @json
// class has zero fields and the writeJSON body ends without a
// `j["Facade"] = thisObject;` line.
func emitImplWriteJSON(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::writeJSON(nlohmann::json& j) {\n", c.ImplName)

	if !c.IsRoot() {
		fmt.Fprintf(w, "\t%s::writeJSON(j);\n\n", c.ImplBase())
	}

	fmt.Fprintf(w, "\tnlohmann::json thisObject = nlohmann::json::object();\n")

	fields := serializableFields(c)

	for _, f := range fields {
		fmt.Fprintf(w, "\tthisObject[%q] = %s;\n\n", f.Name, f.Name)
	}

	if len(fields) > 0 {
		fmt.Fprintf(w, "\tj[%q] = thisObject;\n", c.Name)
	}

	if c.IsRoot() {
		fmt.Fprintf(w, "\tj[\"_className\"] = _className;\n")
	}

	fmt.Fprintf(w, "}\n\n")
}

// emitPODWriteJSON writes FooPOD::writeJSON, gated on @json. POD field
// access is guarded by `if (field)` and emits `field.value()`. Root
// behavior mirrors emitImplWriteJSON. Same empty-class quirk applies:
// the `j[ClassName] = thisObject;` line is skipped when the class has
// no serializable fields.
func emitPODWriteJSON(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::writeJSON(nlohmann::json& j) {\n", c.POD)

	if !c.IsRoot() {
		fmt.Fprintf(w, "\t%s::writeJSON(j);\n\n", c.PODBase())
	}

	fmt.Fprintf(w, "\tnlohmann::json thisObject = nlohmann::json::object();\n")

	fields := serializableFields(c)

	for _, f := range fields {
		fmt.Fprintf(w, "\tif (%s)\n", f.Name)
		fmt.Fprintf(w, "\t\tthisObject[%q] = %s.value();\n\n", f.Name, f.Name)
	}

	if len(fields) > 0 {
		fmt.Fprintf(w, "\tj[%q] = thisObject;\n", c.Name)
	}

	if c.IsRoot() {
		fmt.Fprintf(w, "\tj[\"_className\"] = _className;\n")
	}

	fmt.Fprintf(w, "}\n")
}
