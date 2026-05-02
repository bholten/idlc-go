package sema

import (
	"path"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/hash"
	"github.com/bholten/tools/idlc-go/internal/parser"
)

// Resolve lowers a parsed IDL file into the emit-ready Model.
func Resolve(f *parser.File) (*Model, error) {
	if f == nil || f.Class == nil {
		return nil, errf("nil file or class")
	}

	var pkgParts []string

	if f.Package != "" {
		pkgParts = strings.Split(f.Package, ".")
	}

	className := f.Class.Name
	pathDir := path.Join(pkgParts...)
	idlPath := path.Join(pathDir, className+".idl")

	m := &Model{
		Package:    pkgParts,
		HeaderPath: path.Join(pathDir, className+".h"),
		SourcePath: path.Join(pathDir, className+".cpp"),
		OutputBase: className,
		IDLPath:    idlPath,
		Class: Class{
			Name:       className,
			Doc:        f.Class.Doc,
			Base:       f.Class.Base,
			ImplName:   className + "Implementation",
			Adapter:    className + "Adapter",
			Helper:     className + "Helper",
			POD:        className + "POD",
			HasJSON:    hasAnnotation(f.Class.Annotations, "json"),
			IsMock:     hasAnnotation(f.Class.Annotations, "mock"),
			Implements: f.Class.Implements,
		},
	}

	for _, imp := range f.Imports {
		m.Imports = append(m.Imports, imp.Path)
	}

	for _, inc := range f.Includes {
		m.Includes = append(m.Includes, inc.Path)
	}

	// Class-level annotations that propagate to every method body
	// (`@dirty class` makes every method behave as `@dirty`, etc.).
	classIsDirty := hasAnnotation(f.Class.Annotations, "dirty")

	// Lower members in source order.
	firstSeedAssigned := false

	for _, mem := range f.Class.Members {
		switch v := mem.(type) {
		case *parser.Field:
			fld := lowerField(className, v)
			m.Class.Fields = append(m.Class.Fields, fld)
			if fld.Dereferenced {
				m.Class.DereferencedFieldNames = append(m.Class.DereferencedFieldNames, fld.Name)
			}
		case *parser.Constructor:
			m.Class.Ctors = append(m.Class.Ctors, lowerCtor(v))
		case *parser.Method:
			meth := lowerMethod(v)
			if classIsDirty {
				meth.IsDirty = true
			}
			meth.RPCName = rpcSymbol(meth.Name, meth.Params)

			// First emittable RPC method gets an explicit seed if the
			// legacy CSV has one. Subsequent methods are sequential
			// auto-incremented C++ enum values.
			if !meth.IsLocal && !firstSeedAssigned {
				if seed, ok := LookupSeed(pkgParts, className, meth.Name, meth.Params); ok {
					seed := seed
					meth.RPCSeed = &seed
				}
				firstSeedAssigned = true
			}
			m.Class.Methods = append(m.Class.Methods, meth)
		}
	}

	return m, nil
}

func lowerField(className string, f *parser.Field) Field {
	hashInput := className + "." + f.Name
	isConst := f.Static && f.Final && f.Default != "" && IsPrimitive(f.Type.Name)

	return Field{
		Name:         f.Name,
		IDLType:      f.Type,
		HashInput:    hashInput,
		Hash:         hash.NameHash(hashInput),
		Vis:          f.Visibility,
		Transient:    f.Transient,
		Static:       f.Static && !isConst,
		Final:        f.Final,
		Dereferenced: hasAnnotation(f.Annotations, "dereferenced"),
		WeakRef:      hasAnnotation(f.Annotations, "weakReference"),
		RawTemplate:  annotationArg(f.Annotations, "rawTemplate", "value"),
		IsConst:      isConst,
		Default:      f.Default,
	}
}

func lowerCtor(c *parser.Constructor) Ctor {
	return Ctor{
		Name:   c.Name,
		Params: lowerParams(c.Params),
		Body:   c.Body,
	}
}

func lowerMethod(m *parser.Method) Method {
	return Method{
		Name:                          m.Name,
		Return:                        m.Return,
		Params:                        lowerParams(m.Params),
		Visibility:                    m.Visibility,
		Doc:                           m.Doc,
		IsRead:                        hasAnnotation(m.Annotations, "read"),
		IsLocal:                       hasAnnotation(m.Annotations, "local"),
		IsDirty:                       hasAnnotation(m.Annotations, "dirty"),
		IsPreLocked:                   hasAnnotation(m.Annotations, "preLocked"),
		IsArg1PreLocked:               hasAnnotation(m.Annotations, "arg1preLocked"),
		IsArg2PreLocked:               hasAnnotation(m.Annotations, "arg2preLocked"),
		IsNativeStub:                  hasAnnotation(m.Annotations, "nativeStub"),
		IsVirtualStub:                 hasAnnotation(m.Annotations, "virtualStub"),
		IsNoImplementationDeclaration: hasAnnotation(m.Annotations, "noImplementationDeclaration"),
		IsReference:                   hasAnnotation(m.Annotations, "reference"),
		IsWeakReference:               hasAnnotation(m.Annotations, "weakReference"),
		IsDereferenced:                hasAnnotation(m.Annotations, "dereferenced"),
		IsMock:                        hasAnnotation(m.Annotations, "mock"),
		RawTemplate:                   annotationArg(m.Annotations, "rawTemplate", "value"),
		IsNative:                      m.Native,
		IsAbstract:                    m.Abstract,
		Synchronized:                  m.Synchronized,
		IsFinal:                       m.Final,
		Body:                          m.Body,
	}
}

func lowerParams(in []parser.Param) []Param {
	if len(in) == 0 {
		return nil
	}

	out := make([]Param, len(in))

	for i, p := range in {
		out[i] = Param{
			Name:         p.Name,
			IDLType:      p.Type,
			Final:        p.Final,
			Default:      p.Default,
			Dereferenced: hasAnnotation(p.Annotations, "dereferenced"),
			RawTemplate:  annotationArg(p.Annotations, "rawTemplate", "value"),
		}
	}

	return out
}

type semaError struct{ msg string }

func (e *semaError) Error() string { return e.msg }

func errf(msg string) error { return &semaError{msg: msg} }
