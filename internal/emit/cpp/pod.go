package cpp

import (
	"fmt"
	"io"

	"github.com/bholten/tools/idlc-go/internal/sema"
)

// emitPODHeader writes the FooPOD class declaration.
func emitPODHeader(w io.Writer, m *sema.Model) {
	c := m.Class
	emitDocComment(w, c.Doc)
	fmt.Fprintf(w, "class %s : public %s {\n", c.POD, c.PODBase())
	fmt.Fprintf(w, "public:\n")
	for _, f := range serializableFields(c) {
		fmt.Fprintf(w, "\tOptional<%s> %s;\n\n", sema.CppRenderPODFieldType(f, m.Registry), f.Name)
	}
	if hasNoFields(c) {
		// No-fields POD: JAR emits a blank line in lieu of the
		// `String _className;` field that fielded PODs carry.
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "\tString _className;\n")
	}
	fmt.Fprintf(w, "\t%s();\n", c.POD)
	if c.HasJSON {
		fmt.Fprintf(w, "\tvirtual void writeJSON(nlohmann::json& j);\n")
	}
	fmt.Fprintf(w, "\tvirtual void readObject(ObjectInputStream* stream);\n")
	fmt.Fprintf(w, "\tvirtual void writeObject(ObjectOutputStream* stream);\n")
	fmt.Fprintf(w, "\tbool readObjectMember(ObjectInputStream* stream, const uint32& nameHashCode);\n")
	fmt.Fprintf(w, "\tint writeObjectMembers(ObjectOutputStream* stream);\n")
	fmt.Fprintf(w, "\tvoid writeObjectCompact(ObjectOutputStream* stream);\n")
	fmt.Fprintf(w, "\n\n\n")
	fmt.Fprintf(w, "\tvirtual ~%s();\n\n", c.POD)
	fmt.Fprintf(w, "};\n\n")
}

func emitPODSource(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "/*\n")
	fmt.Fprintf(w, " *\t%s\n", c.POD)
	fmt.Fprintf(w, " */\n\n")

	fmt.Fprintf(w, "%s::~%s() {\n", c.POD, c.POD)
	// JAR quirk: when ANY class in the IDL inheritance chain declares
	// `finalize()`, the POD dtor calls the unqualified `finalize();`
	// so the POD's destruction goes through the same path as the
	// impl's. The impl-side dtor's `finalize();` is LOCAL only (this
	// class declared it), but the POD's is TRANSITIVE — surfaced when
	// running over the full Core3 corpus (e.g. ArmorComponent ←
	// Component ← TangibleObject ← SceneObject, where SceneObject
	// declares finalize).
	if hasIDLFinalize(c) || (m.Registry != nil && m.Registry.HasTransitiveFinalize(c.Name)) {
		fmt.Fprintf(w, "\tfinalize();\n")
	}
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "%s::%s(void) {\n", c.POD, c.POD)
	fmt.Fprintf(w, "\t_className = \"%s\";\n", c.Name)
	fmt.Fprintf(w, "}\n\n\n")

	if c.HasJSON {
		emitPODWriteJSON(w, c)
		fmt.Fprintf(w, "\n\n")
	}

	fmt.Fprintf(w, "void %s::writeObject(ObjectOutputStream* stream) {\n", c.POD)
	fmt.Fprintf(w, "\tint _currentOffset = stream->getOffset();\n")
	fmt.Fprintf(w, "\tstream->writeShort(0);\n")
	fmt.Fprintf(w, "\tint _varCount = %s::writeObjectMembers(stream);\n", c.POD)
	fmt.Fprintf(w, "\tstream->writeShort(_currentOffset, _varCount);\n")
	fmt.Fprintf(w, "}\n\n")

	emitPODWriteObjectMembers(w, m)
	emitPODReadObjectMember(w, m)
	emitPODReadObject(w, c)
	emitPODWriteObjectCompact(w, m)
}

func emitPODWriteObjectMembers(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "int %s::writeObjectMembers(ObjectOutputStream* stream) {\n", c.POD)
	if c.IsRoot() {
		fmt.Fprintf(w, "\tint _count = 0;\n")
	} else {
		fmt.Fprintf(w, "\tint _count = %s::writeObjectMembers(stream);\n\n", c.PODBase())
	}
	fmt.Fprintf(w, "\tuint32 _nameHashCode;\n")
	fmt.Fprintf(w, "\tint _offset;\n")
	fmt.Fprintf(w, "\tuint32 _totalSize;\n")
	for _, f := range serializableFields(c) {
		fmt.Fprintf(w, "\tif (%s) {\n", f.Name)
		fmt.Fprintf(w, "\t_nameHashCode = 0x%x; //%s\n", f.Hash, f.HashInput)
		fmt.Fprintf(w, "\tTypeInfo<uint32>::toBinaryStream(&_nameHashCode, stream);\n")
		fmt.Fprintf(w, "\t_offset = stream->getOffset();\n")
		fmt.Fprintf(w, "\tstream->writeInt(0);\n")
		fmt.Fprintf(w, "\tTypeInfo<%s >::toBinaryStream(&%s.value(), stream);\n", sema.CppRenderPODFieldType(f, m.Registry), f.Name)
		fmt.Fprintf(w, "\t_totalSize = (uint32) (stream->getOffset() - (_offset + 4));\n")
		fmt.Fprintf(w, "\tstream->writeInt(_offset, _totalSize);\n")
		fmt.Fprintf(w, "\t_count++;\n")
		fmt.Fprintf(w, "\t}\n\n")
	}
	if c.IsRoot() {
		fmt.Fprintf(w, "\n\t_nameHashCode = 0x%x;//%s\n", classNameFieldHash, classNameFieldHashInput)
		fmt.Fprintf(w, "\tTypeInfo<uint32>::toBinaryStream(&_nameHashCode, stream);\n")
		fmt.Fprintf(w, "\t_offset = stream->getOffset();\n")
		fmt.Fprintf(w, "\tstream->writeInt(0);\n")
		fmt.Fprintf(w, "\tTypeInfo<String>::toBinaryStream(&_className, stream);\n")
		fmt.Fprintf(w, "\t_totalSize = (uint32) (stream->getOffset() - (_offset + 4));\n")
		fmt.Fprintf(w, "\tstream->writeInt(_offset, _totalSize);\n")
		fmt.Fprintf(w, "\treturn _count + 1;\n")
	} else {
		fmt.Fprintf(w, "\n\treturn _count;\n")
	}
	fmt.Fprintf(w, "}\n\n")
}

func emitPODReadObjectMember(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "bool %s::readObjectMember(ObjectInputStream* stream, const uint32& nameHashCode) {\n", c.POD)
	if c.IsRoot() {
		fmt.Fprintf(w, "\tif (nameHashCode == 0x%x) {//%s \n", classNameFieldHash, classNameFieldHashInput)
		fmt.Fprintf(w, "\t\tTypeInfo<String>::parseFromBinaryStream(&_className, stream);\n")
		fmt.Fprintf(w, "\t\treturn true;\n")
		fmt.Fprintf(w, "\t}\n\n")
	} else {
		fmt.Fprintf(w, "\tif (%s::readObjectMember(stream, nameHashCode))\n", c.PODBase())
		fmt.Fprintf(w, "\t\treturn true;\n\n")
	}
	fmt.Fprintf(w, "\tswitch(nameHashCode) {\n")
	for _, f := range serializableFields(c) {
		typ := sema.CppRenderPODFieldType(f, m.Registry)
		fmt.Fprintf(w, "\tcase 0x%x: //%s\n", f.Hash, f.HashInput)
		fmt.Fprintf(w, "\t\t{\n")
		fmt.Fprintf(w, "\t\t\t%s _mn%s;\n", typ, f.Name)
		fmt.Fprintf(w, "\t\t\tTypeInfo<%s >::parseFromBinaryStream(&_mn%s, stream);\n", typ, f.Name)
		fmt.Fprintf(w, "\t\t\t%s = std::move(_mn%s);\n", f.Name, f.Name)
		fmt.Fprintf(w, "\t\t}\n")
		fmt.Fprintf(w, "\t\treturn true;\n\n")
	}
	fmt.Fprintf(w, "\t}\n\n")
	fmt.Fprintf(w, "\treturn false;\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitPODReadObject(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::readObject(ObjectInputStream* stream) {\n", c.POD)
	fmt.Fprintf(w, "\tuint16 _varCount = stream->readShort();\n")
	fmt.Fprintf(w, "\tfor (int i = 0; i < _varCount; ++i) {\n")
	fmt.Fprintf(w, "\t\tuint32 _nameHashCode;\n")
	fmt.Fprintf(w, "\t\tTypeInfo<uint32>::parseFromBinaryStream(&_nameHashCode, stream);\n\n")
	fmt.Fprintf(w, "\t\tuint32 _varSize = stream->readInt();\n\n")
	fmt.Fprintf(w, "\t\tint _currentOffset = stream->getOffset();\n\n")
	fmt.Fprintf(w, "\t\tif(%s::readObjectMember(stream, _nameHashCode)) {\n", c.POD)
	fmt.Fprintf(w, "\t\t}\n\n")
	fmt.Fprintf(w, "\t\tstream->setOffset(_currentOffset + _varSize);\n")
	fmt.Fprintf(w, "\t}\n\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitPODWriteObjectCompact(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "void %s::writeObjectCompact(ObjectOutputStream* stream) {\n", c.POD)
	if !c.IsRoot() {
		fmt.Fprintf(w, "\t%s::writeObjectCompact(stream);\n\n", c.PODBase())
	}
	for _, f := range serializableFields(c) {
		fmt.Fprintf(w, "\tTypeInfo<%s >::toBinaryStream(&%s.value(), stream);\n\n", sema.CppRenderPODFieldType(f, m.Registry), f.Name)
	}
	fmt.Fprintf(w, "\n}\n\n")
}
