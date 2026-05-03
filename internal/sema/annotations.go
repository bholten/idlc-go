package sema

import "github.com/bholten/idlc-go/internal/parser"

func hasAnnotation(anns []parser.Annotation, name string) bool {
	for _, a := range anns {
		if a.Name == name {
			return true
		}
	}

	return false
}

// annotationArg returns the value of a named arg on the first
// annotation matching name (e.g. `@rawTemplate(value = "X*")` →
// annotationArg(anns, "rawTemplate", "value") = "X*"). Returns "" if
// the annotation isn't present or has no such arg.
func annotationArg(anns []parser.Annotation, name, key string) string {
	for _, a := range anns {
		if a.Name == name {
			if a.Args == nil {
				return ""
			}

			return a.Args[key]
		}
	}

	return ""
}
