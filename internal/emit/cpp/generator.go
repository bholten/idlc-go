// Package cpp emits C++ headers and sources for one IDL class.
//
// The emitter is intentionally explicit and uses fmt.Fprintf rather than
// templates. The output is judged by literal `git diff` against the JAR's
// autogen tree, so making the emit code closely mirror the target output
// (whitespace, ordering, blank-line counts) is the priority.
package cpp

import (
	"bytes"

	"github.com/bholten/idlc-go/internal/sema"
)

// Generate produces the .h and .cpp file contents for one model.
//
// The optional registry tells the emitter which `import` qnames refer
// to other IDL-defined classes (forward-declarable in the .h, included
// in the .cpp; rendered as `ManagedReference<T*>` rather than
// `Reference<T*>` in fields/method generics). Pass nil if no registry
// is available — every import is then treated as a non-IDL include.
func Generate(m *sema.Model, reg *sema.Registry) (header, source []byte, err error) {
	m.Registry = reg
	var hb, sb bytes.Buffer

	emitHeader(&hb, m, reg)
	emitSource(&sb, m, reg)

	return hb.Bytes(), sb.Bytes(), nil
}
