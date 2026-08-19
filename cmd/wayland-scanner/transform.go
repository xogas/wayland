package main

// This file converts the parsed protocol model (protocol.go) into the
// Go-specific view that templates.go renders. The conversion is pure:
// it reads the Protocol model and produces template data, touching no
// filesystem.

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// wireType describes how a Wayland wire type maps to Go.
type wireType struct {
	goType string // field type in message structs
	read   string // call on wire.Reader
	write  string // method on wire.Writer
}

// wireTypes maps every Wayland wire type to its Go representation.
var wireTypes = map[string]wireType{
	"int":    {"int32", "r.Int32()", "Int32"},
	"uint":   {"uint32", "r.Uint32()", "Uint32"},
	"fixed":  {"wire.Fixed", "r.Fixed()", "Fixed"},
	"string": {"string", "r.String()", "String"},
	"object": {"wire.ObjectID", "r.Object()", "Object"},
	"new_id": {"wire.NewID", "r.NewID()", "NewID"},
	"array":  {"[]byte", "r.Array()", "Array"},
	"fd":     {"int", "r.Fd()", "Fd"},
}

// GoInterface is the Go-specific view of one protocol Interface.
type GoInterface struct {
	Package  string
	TypeName string // PascalCase Go type name, e.g. "Display"
	IfName   string // protocol interface name, e.g. "wl_display"
	Version  int
	Imports  []string // packages imported by the generated file
	Doc      string   // rendered doc comment, "" if none

	Enums    []GoEnum
	Requests []GoRequest
	Events   []GoEvent

	EventFDCounts map[uint16]int // opcode -> fd count (0 = no fds)
	WaylandPkg    string         // "wayland." for sub-packages, "" for the core package
}

// HasEvents reports whether the interface has events.
func (g GoInterface) HasEvents() bool { return len(g.Events) > 0 }

// LowerTypeName is TypeName in all lowercase, for unexported generated identifiers.
func (g GoInterface) LowerTypeName() string { return strings.ToLower(g.TypeName) }

// GoRequest is the Go-specific view of a protocol Request.
type GoRequest struct {
	TypeName        string // Go type name of the owning interface
	Name            string
	Opcode          int
	Since           int
	DeprecatedSince int // 0 means not deprecated
	StructDoc       string
	MethodDoc       string
	Args            []GoArg

	// NewIDType is the type of the object this request creates; "" when
	// there is no new_id or its interface is not part of this protocol.
	NewIDType string

	// CrossNewID marks a new_id for an interface outside this protocol;
	// the method returns *Proxy instead of a concrete type.
	CrossNewID bool

	// SynthVersion marks a new_id without an interface attribute
	// (e.g. wl_registry.bind); the method takes synthetic interface
	// and version parameters.
	SynthVersion bool

	// Destructor marks a destroy request.
	Destructor bool
}

// OpName is the name of the opcode constant.
func (r GoRequest) OpName() string { return r.TypeName + "Request" + r.Name }

// StructName is the name of the request message struct.
func (r GoRequest) StructName() string { return r.TypeName + r.Name + "Request" }

// MethodParams renders the method parameter list. new_id arguments are
// created inside the method and never appear as parameters; adjacent
// parameters of the same type are merged, e.g. "x, y int32".
func (r GoRequest) MethodParams() string {
	var parts, group []string
	var groupType string
	flush := func() {
		if len(group) > 0 {
			parts = append(parts, strings.Join(group, ", ")+" "+groupType)
			group = nil
		}
	}
	for _, a := range r.Args {
		if a.IsNewID() {
			continue
		}
		if t := a.Type(); t != groupType {
			flush()
			groupType = t
		}
		group = append(group, a.ParamName)
	}
	flush()
	return strings.Join(parts, ", ")
}

// GoEvent is the Go-specific view of a protocol Event.
type GoEvent struct {
	TypeName        string
	Name            string
	Opcode          int
	Since           int
	DeprecatedSince int // 0 means not deprecated
	StructDoc       string
	Args            []GoArg
}

// OpName is the name of the opcode constant.
func (ev GoEvent) OpName() string { return ev.TypeName + "Event" + ev.Name }

// StructName is the name of the event message struct.
func (ev GoEvent) StructName() string { return ev.TypeName + ev.Name + "Event" }

// FuncName is the name of the event callback type.
func (ev GoEvent) FuncName() string { return ev.TypeName + ev.Name + "Func" }

// HasNewID reports whether the event carries a new_id argument with a
// resolvable interface.
func (ev GoEvent) HasNewID() bool {
	for _, a := range ev.Args {
		if a.NewIDType != "" {
			return true
		}
	}
	return false
}

// GoArg is the Go-specific view of one argument.
type GoArg struct {
	GoName    string // struct field name
	ParamName string // method parameter name
	Wire      string // Wayland wire type, e.g. "uint" or "new_id"
	EnumType  string // Go enum type when the argument references a known enum
	NewIDType string // Go interface type for a resolvable new_id
	AllowNull bool
	Doc       string // field doc comment, "" if none
}

// wt returns the wire mapping for the argument, handling the nullable
// string variant.
func (a GoArg) wt() wireType {
	if a.Wire == "string" && a.AllowNull {
		return wireType{"*string", "r.StringNullable()", "StringNullable"}
	}
	return wireTypes[a.Wire]
}

// GoType is the wire Go type used in message structs.
func (a GoArg) GoType() string { return a.wt().goType }

// Type is the effective Go type: a pointer to the interface type for a
// resolvable new_id, the enum type, or the wire type.
func (a GoArg) Type() string {
	if a.NewIDType != "" {
		return "*" + a.NewIDType
	}
	if a.EnumType != "" {
		return a.EnumType
	}
	return a.GoType()
}

// Read is the wire.Reader call that decodes the argument.
func (a GoArg) Read() string { return a.wt().read }

// Write is the wire.Writer method that encodes the argument.
func (a GoArg) Write() string { return a.wt().write }

// IsNewID reports whether the argument is a new_id.
func (a GoArg) IsNewID() bool { return a.Wire == "new_id" }

// EnumCast wraps expr in a conversion to the enum type, or returns expr
// unchanged when the argument is not enum-typed.
func (a GoArg) EnumCast(expr string) string {
	if a.EnumType == "" {
		return expr
	}
	return a.EnumType + "(" + expr + ")"
}

// MarshalExpr renders the value expression passed to the wire writer,
// converting enum-typed arguments back to their wire type.
func (a GoArg) MarshalExpr() string {
	if a.EnumType == "" {
		return "r." + a.GoName
	}
	return a.GoType() + "(r." + a.GoName + ")"
}

// GoEnum is the Go-specific view of an enum.
type GoEnum struct {
	Type       string
	IsBitField bool
	Doc        string
	Entries    []GoEnumEntry
}

// GoEnumEntry is the Go-specific view of an enum entry.
type GoEnumEntry struct {
	Const string
	Val   string
	Doc   string
}

// enumMap maps enum references to Go type names. Keys are short names
// ("format") for same-interface enums or qualified names ("wl_shm.format")
// for cross-interface enums.
type enumMap map[string]string

// docComment renders a Go doc comment for an exported identifier: name,
// lower-cased summary, an optional "Deprecated:" note, then the dedented
// description body. Returns "" when there is nothing to document.
func docComment(name, summary, text string, deprecatedSince int) string {
	if strings.TrimSpace(summary) == "" && strings.TrimSpace(text) == "" && deprecatedSince == 0 {
		return ""
	}
	first := name
	if s := strings.Join(strings.Fields(summary), " "); s != "" {
		first += " " + lowerFirst(s)
	}
	first = strings.TrimRight(first, ". 	") + "."

	var lines []string
	lines = append(lines, "// "+first)
	if deprecatedSince > 0 {
		lines = append(lines, "//")
		lines = append(lines, fmt.Sprintf("// Deprecated: since version %d.", deprecatedSince))
	}
	if t := dedent(text); t != "" {
		lines = append(lines, "//")
		for _, ln := range strings.Split(t, "\n") {
			ln = strings.TrimRight(ln, " 	")
			if ln == "" {
				lines = append(lines, "//")
			} else {
				lines = append(lines, "// "+ln)
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// fieldComment renders a single-line doc comment for a struct field.
func fieldComment(name, summary string) string {
	s := strings.Join(strings.Fields(summary), " ")
	if s == "" {
		return ""
	}
	return "// " + name + " " + lowerFirst(s) + ".\n"
}

// lowerFirst lower-cases the first rune of s.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// dedent strips the common leading indentation and leading/trailing blank
// lines. XML description bodies are uniformly indented, so the stripped
// comment text stays flush left (gofmt would mangle indented comments).
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	minIndent := -1
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n := len(ln) - len(strings.TrimLeft(ln, " 	"))
		if minIndent == -1 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent > 0 {
		for i, ln := range lines {
			if len(ln) >= minIndent {
				lines[i] = ln[minIndent:]
			}
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// buildEnumMap builds the protocol-wide enum reference to Go type mapping.
func buildEnumMap(ifaces []Interface, prefix string) enumMap {
	m := make(enumMap)
	for i := range ifaces {
		iface := &ifaces[i]
		tn := typeName(iface.Name, prefix)
		for j := range iface.Enums {
			e := &iface.Enums[j]
			goType := tn + pascal(e.Name)
			m[e.Name] = goType
			m[iface.Name+"."+e.Name] = goType
		}
	}
	return m
}

// convertInterface converts one Interface into its Go-specific view.
func convertInterface(iface *Interface, pkg, prefix string, knownIface map[string]bool, em enumMap) (GoInterface, error) {
	tn := typeName(iface.Name, prefix)

	isRoot := pkg == corePkg
	g := GoInterface{
		Package:  pkg,
		TypeName: tn,
		IfName:   iface.Name,
		Version:  iface.Version,
		Doc:      docComment(tn, iface.Description.Summary, iface.Description.Text, 0),
	}
	if !isRoot {
		g.WaylandPkg = "wayland."
	}

	g.Enums = buildEnums(iface, tn)
	var err error
	g.Requests, err = buildRequests(iface, tn, prefix, knownIface, em)
	if err != nil {
		return GoInterface{}, fmt.Errorf("interface %s: %w", iface.Name, err)
	}
	g.Events, g.EventFDCounts, err = buildEvents(iface, tn, prefix, knownIface, em)
	if err != nil {
		return GoInterface{}, fmt.Errorf("interface %s: %w", iface.Name, err)
	}
	g.Imports = buildImports(len(g.Requests)+len(g.Events) > 0, isRoot)

	return g, nil
}

// buildEnums converts Interface enums to GoEnums.
func buildEnums(iface *Interface, tn string) []GoEnum {
	var out []GoEnum
	for i := range iface.Enums {
		e := &iface.Enums[i]
		en := GoEnum{
			Type:       tn + pascal(e.Name),
			IsBitField: e.BitField,
			Doc:        docComment(tn+pascal(e.Name), e.Description.Summary, e.Description.Text, 0),
		}
		for j := range e.Entries {
			entry := &e.Entries[j]
			en.Entries = append(en.Entries, GoEnumEntry{
				Const: en.Type + pascal(entry.Name),
				Val:   fmt.Sprintf("%d", entry.Value),
				Doc:   docComment(en.Type+pascal(entry.Name), entry.Summary, entry.Description.Text, entry.DeprecatedSince),
			})
		}
		out = append(out, en)
	}
	return out
}

// buildRequests converts Interface requests to GoRequests.
func buildRequests(iface *Interface, tn, prefix string, knownIface map[string]bool, em enumMap) ([]GoRequest, error) {
	var out []GoRequest
	for opcode := range iface.Requests {
		r := &iface.Requests[opcode]
		name := pascal(r.Name)
		rd := GoRequest{
			TypeName:        tn,
			Name:            name,
			Opcode:          opcode,
			Since:           max(r.Since, 1),
			DeprecatedSince: r.DeprecatedSince,
			StructDoc:       docComment(tn+name+"Request", r.Description.Summary, r.Description.Text, r.DeprecatedSince),
			MethodDoc:       docComment(name, r.Description.Summary, r.Description.Text, r.DeprecatedSince),
			Destructor:      r.Type == "destructor",
		}

		for j := range r.Args {
			ga, err := buildArg(&r.Args[j], em)
			if err != nil {
				return nil, fmt.Errorf("request %s: %w", r.Name, err)
			}
			// A new_id without an interface attribute needs synthetic
			// interface and version arguments injected.
			if ga.IsNewID() && r.Args[j].Interface == "" {
				rd.SynthVersion = true
				rd.Args = append(rd.Args,
					GoArg{GoName: "Interface", ParamName: "interface_", Wire: "string"},
					GoArg{GoName: "Version", ParamName: "version", Wire: "uint"},
				)
			}
			rd.Args = append(rd.Args, ga)

			if !ga.IsNewID() {
				continue
			}
			if ifn := r.Args[j].Interface; ifn != "" && knownIface[ifn] {
				rd.NewIDType = typeName(ifn, prefix)
			} else {
				rd.CrossNewID = true
			}
		}
		out = append(out, rd)
	}
	return out, nil
}

// buildEvents converts Interface events to GoEvents and the opcode -> fd
// count table (0 for events without fds).
func buildEvents(iface *Interface, tn, prefix string, knownIface map[string]bool, em enumMap) ([]GoEvent, map[uint16]int, error) {
	var events []GoEvent
	fdCounts := make(map[uint16]int, len(iface.Events))
	for opcode := range iface.Events {
		e := &iface.Events[opcode]
		name := pascal(e.Name)
		ed := GoEvent{
			TypeName:        tn,
			Name:            name,
			Opcode:          opcode,
			Since:           max(e.Since, 1),
			DeprecatedSince: e.DeprecatedSince,
			StructDoc:       docComment(tn+name+"Event", e.Description.Summary, e.Description.Text, e.DeprecatedSince),
		}
		fdCount := 0
		for j := range e.Args {
			ga, err := buildArg(&e.Args[j], em)
			if err != nil {
				return nil, nil, fmt.Errorf("event %s: %w", e.Name, err)
			}
			if ga.IsNewID() && e.Args[j].Interface != "" && knownIface[e.Args[j].Interface] {
				ga.NewIDType = typeName(e.Args[j].Interface, prefix)
			}
			ed.Args = append(ed.Args, ga)
			if e.Args[j].Type == "fd" {
				fdCount++
			}
		}
		fdCounts[uint16(opcode)] = fdCount
		events = append(events, ed)
	}
	return events, fdCounts, nil
}

// buildArg maps a parsed Arg to its Go-specific view.
func buildArg(a *Arg, em enumMap) (GoArg, error) {
	if _, ok := wireTypes[a.Type]; !ok {
		return GoArg{}, fmt.Errorf("unknown arg type %q", a.Type)
	}
	ad := GoArg{
		GoName:    pascal(a.Name),
		ParamName: camel(a.Name),
		Wire:      a.Type,
		AllowNull: a.AllowNull,
		Doc:       fieldComment(pascal(a.Name), a.Summary),
	}
	// Enums only apply to uint32 arguments; int32-based enums would need a
	// matching enum base type.
	if a.Enum != "" && wireTypes[a.Type].goType == "uint32" {
		ad.EnumType = em[a.Enum]
	}
	return ad, nil
}

// buildImports lists the packages a generated file imports: the wire codec
// when the interface has requests or events, and the core wayland package
// for extension protocols.
func buildImports(hasWire, isRoot bool) []string {
	var imps []string
	if hasWire {
		imps = append(imps, "github.com/xogas/wayland/wire")
	}
	if !isRoot {
		imps = append(imps, "github.com/xogas/wayland")
	}
	return imps
}
