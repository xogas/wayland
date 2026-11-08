package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Turns the protocol tree into the data templates render.

type wireType struct {
	goType string // struct field type
	read   string // wire.Reader call
	write  string // wire.Writer method
}

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

const (
	wirePkgPath = "github.com/xogas/wayland/wire"
	corePkgPath = "github.com/xogas/wayland"
)

type goInterface struct {
	Package  string
	TypeName string // Go type name, e.g. "Display"
	IfName   string // protocol name, e.g. "wl_display"
	Version  int
	Frozen   bool
	Imports  []string
	Doc      string

	Enums    []goEnum
	Requests []goRequest
	Events   []goEvent

	EventFDCounts map[uint16]int
	WaylandPkg    string // "wayland." outside the core package, else ""
}

func (g goInterface) HasEvents() bool { return len(g.Events) > 0 }

func (g goInterface) LowerTypeName() string { return strings.ToLower(g.TypeName) }

type goRequest struct {
	TypeName  string
	Name      string
	Opcode    int
	Since     int
	StructDoc string
	MethodDoc string
	Args      []goArg

	NewIDType    string // interface created by this request, "" if none
	CrossNewID   bool   // new_id from another protocol: returns *Proxy
	SynthVersion bool   // no interface attr (wl_registry.bind): caller supplies it

	Destructor bool
}

func (r goRequest) OpName() string     { return r.TypeName + "Request" + r.Name }
func (r goRequest) StructName() string { return r.TypeName + r.Name + "Request" }

// new_id arguments are created inside the method, so they are not parameters.
func (r goRequest) MethodParams() string {
	var parts []string
	var group []string
	groupType := ""

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
		if a.Type() != groupType {
			flush()
			groupType = a.Type()
		}
		group = append(group, a.ParamName)
	}
	flush()
	return strings.Join(parts, ", ")
}

type goEvent struct {
	TypeName  string
	Name      string
	Opcode    int
	Since     int
	StructDoc string
	Args      []goArg
}

func (ev goEvent) OpName() string     { return ev.TypeName + "Event" + ev.Name }
func (ev goEvent) StructName() string { return ev.TypeName + ev.Name + "Event" }
func (ev goEvent) FuncName() string   { return ev.TypeName + ev.Name + "Func" }

// Such an event cannot call Unmarshal: building the child needs the Conn.
func (ev goEvent) HasNewID() bool {
	return slices.ContainsFunc(ev.Args, func(a goArg) bool { return a.NewIDType != "" })
}

type goArg struct {
	GoName    string // struct field
	ParamName string // method parameter
	Wire      string // Wayland wire type
	EnumType  string // Go enum type, when it references a known enum
	NewIDType string // Go wrapper type for a resolvable new_id
	AllowNull bool
	Doc       string
}

func (a goArg) wire() wireType {
	// Nullable string is the only type whose Go form depends on an attribute.
	if a.Wire == "string" && a.AllowNull {
		return wireType{"*string", "r.StringNullable()", "StringNullable"}
	}
	return wireTypes[a.Wire]
}

func (a goArg) Type() string {
	switch {
	case a.NewIDType != "":
		return "*" + a.NewIDType
	case a.EnumType != "":
		return a.EnumType
	}
	return a.wire().goType
}

func (a goArg) Read() string  { return a.wire().read }
func (a goArg) Write() string { return a.wire().write }
func (a goArg) IsNewID() bool { return a.Wire == "new_id" }

func (a goArg) EnumCast(expr string) string {
	if a.EnumType == "" {
		return expr
	}
	return a.EnumType + "(" + expr + ")"
}

func (a goArg) MarshalExpr() string {
	if a.EnumType == "" {
		return "r." + a.GoName
	}
	return a.wire().goType + "(r." + a.GoName + ")"
}

type goEnum struct {
	Type       string
	IsBitField bool
	Since      int
	Doc        string
	Entries    []goEnumEntry
}

type goEnumEntry struct {
	Const string
	Val   string
	Since int
	Doc   string
}

type gen struct {
	pkg    string
	prefix string
	known  map[string]bool   // interfaces declared in this file
	enums  map[string]string // short and qualified name -> Go type
}

func buildProtocol(p *Protocol, pkg, prefix string) []goInterface {
	g := &gen{
		pkg:    pkg,
		prefix: prefix,
		known:  make(map[string]bool, len(p.Interfaces)),
		enums:  make(map[string]string),
	}
	for i := range p.Interfaces {
		in := &p.Interfaces[i]
		g.known[in.Name] = true
		tn := typeName(in.Name, prefix)
		for j := range in.Enums {
			// Short name for same-interface refs, qualified for cross.
			g.enums[in.Enums[j].Name] = tn + pascal(in.Enums[j].Name)
			g.enums[in.Name+"."+in.Enums[j].Name] = tn + pascal(in.Enums[j].Name)
		}
	}

	out := make([]goInterface, 0, len(p.Interfaces))
	for i := range p.Interfaces {
		out = append(out, g.interfaceView(&p.Interfaces[i]))
	}
	return out
}

func (g *gen) interfaceView(in *Interface) goInterface {
	tn := typeName(in.Name, g.prefix)
	v := goInterface{
		Package:  g.pkg,
		TypeName: tn,
		IfName:   in.Name,
		Version:  in.Version,
		Frozen:   in.Frozen,
		Doc:      docComment(tn, in.Description, 0),
		Enums:    g.enumsOf(in, tn),
	}
	if g.pkg != corePkg {
		v.WaylandPkg = "wayland."
	}

	v.Requests = g.requestsOf(in, tn)
	v.Events, v.EventFDCounts = g.eventsOf(in, tn)
	if len(v.Requests)+len(v.Events) > 0 {
		v.Imports = append(v.Imports, wirePkgPath)
	}
	if g.pkg != corePkg {
		v.Imports = append(v.Imports, corePkgPath)
	}
	return v
}

func (g *gen) enumsOf(in *Interface, tn string) []goEnum {
	out := make([]goEnum, 0, len(in.Enums))
	for i := range in.Enums {
		e := &in.Enums[i]
		en := goEnum{
			Type:       tn + pascal(e.Name),
			IsBitField: e.BitField,
			Since:      max(e.Since, 1),
			Doc:        docComment(tn+pascal(e.Name), e.Description, 0),
		}
		for j := range e.Entries {
			entry := &e.Entries[j]
			en.Entries = append(en.Entries, goEnumEntry{
				Const: en.Type + pascal(entry.Name),
				Val:   fmt.Sprintf("%d", entry.Value),
				Since: max(entry.Since, 1),
				Doc:   docComment(en.Type+pascal(entry.Name), Description{Summary: entry.Summary, Text: entry.Description.Text}, entry.DeprecatedSince),
			})
		}
		out = append(out, en)
	}
	return out
}

func (g *gen) requestsOf(in *Interface, tn string) []goRequest {
	out := make([]goRequest, 0, len(in.Requests))
	for opcode := range in.Requests {
		m := &in.Requests[opcode]
		name := pascal(m.Name)
		r := goRequest{
			TypeName:   tn,
			Name:       name,
			Opcode:     opcode,
			Since:      max(m.Since, 1),
			StructDoc:  docComment(tn+name+"Request", m.Description, m.DeprecatedSince),
			MethodDoc:  docComment(name, m.Description, m.DeprecatedSince),
			Destructor: m.Type == "destructor",
		}
		for j := range m.Args {
			a := &m.Args[j]
			ga := g.argView(a)
			if ga.IsNewID() && a.Interface == "" {
				r.SynthVersion = true
				r.Args = append(r.Args,
					goArg{GoName: "Interface", ParamName: "interface_", Wire: "string"},
					goArg{GoName: "Version", ParamName: "version", Wire: "uint"},
				)
			}
			r.Args = append(r.Args, ga)
			if !ga.IsNewID() {
				continue
			}
			if g.known[a.Interface] {
				r.NewIDType = typeName(a.Interface, g.prefix)
			} else {
				r.CrossNewID = true
			}
		}
		out = append(out, r)
	}
	return out
}

func (g *gen) eventsOf(in *Interface, tn string) ([]goEvent, map[uint16]int) {
	events := make([]goEvent, 0, len(in.Events))
	fdCounts := make(map[uint16]int, len(in.Events))
	for opcode := range in.Events {
		m := &in.Events[opcode]
		name := pascal(m.Name)
		ev := goEvent{
			TypeName:  tn,
			Name:      name,
			Opcode:    opcode,
			Since:     max(m.Since, 1),
			StructDoc: docComment(tn+name+"Event", m.Description, m.DeprecatedSince),
		}
		fds := 0
		for j := range m.Args {
			a := &m.Args[j]
			ga := g.argView(a)
			if ga.IsNewID() && g.known[a.Interface] {
				ga.NewIDType = typeName(a.Interface, g.prefix)
			}
			ev.Args = append(ev.Args, ga)
			if a.Type == "fd" {
				fds++
			}
		}
		fdCounts[uint16(opcode)] = fds
		events = append(events, ev)
	}
	return events, fdCounts
}

func (g *gen) argView(a *Arg) goArg {
	ga := goArg{
		GoName:    pascal(a.Name),
		ParamName: camel(a.Name),
		Wire:      a.Type,
		AllowNull: a.AllowNull,
		Doc:       fieldComment(pascal(a.Name), a.Summary),
	}
	// Wayland enums are uint32; an int-typed arg carrying one stays int32.
	if a.Enum != "" && wireTypes[a.Type].goType == "uint32" {
		ga.EnumType = g.enums[a.Enum]
	}
	return ga
}

func pascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		if p == "id" {
			parts[i] = "ID"
			continue
		}
		r, n := utf8.DecodeRuneInString(p)
		parts[i] = string(unicode.ToUpper(r)) + p[n:]
	}
	return strings.Join(parts, "")
}

func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return p
	}
	var result string
	isID := strings.HasPrefix(p, "ID") && (len(p) == 2 || (p[2] >= 'A' && p[2] <= 'Z'))
	if isID {
		result = "id" + p[2:]
	} else {
		r, n := utf8.DecodeRuneInString(p)
		result = string(unicode.ToLower(r)) + p[n:]
	}
	if goKeywords[result] {
		return result + "_"
	}
	return result
}

func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func typeName(xmlName, prefix string) string {
	return pascal(strings.TrimPrefix(xmlName, prefix))
}

func autoPrefix(interfaces []Interface) string {
	if len(interfaces) == 0 {
		return ""
	}
	common := interfaces[0].Name
	for _, in := range interfaces[1:] {
		n := min(len(common), len(in.Name))
		i := 0
		for i < n && common[i] == in.Name[i] {
			i++
		}
		if common = common[:i]; i == 0 {
			return ""
		}
	}
	if idx := strings.IndexByte(common, '_'); idx >= 0 {
		return common[:idx+1]
	}
	return ""
}

// Keywords and predeclared identifiers that must not become parameter names.
var goKeywords = map[string]bool{
	"break": true, "default": true, "func": true, "interface": true, "select": true,
	"case": true, "defer": true, "go": true, "map": true, "struct": true,
	"chan": true, "else": true, "goto": true, "package": true, "switch": true,
	"const": true, "fallthrough": true, "if": true, "range": true, "type": true,
	"continue": true, "for": true, "import": true, "return": true, "var": true,
	"error": true, "len": true, "cap": true, "string": true, "nil": true,
}

// Returns "" when there is nothing to say.
func docComment(name string, d Description, deprecatedSince int) string {
	body := dedent(d.Text)
	if strings.TrimSpace(d.Summary) == "" && body == "" && deprecatedSince == 0 {
		return ""
	}

	first := name
	if s := strings.Join(strings.Fields(d.Summary), " "); s != "" {
		first += " " + lowerFirst(s)
	}
	lines := []string{"// " + strings.TrimRight(first, ". \t") + "."}

	if deprecatedSince > 0 {
		lines = append(lines, "//", fmt.Sprintf("// Deprecated: since version %d.", deprecatedSince))
	}
	if body != "" {
		lines = append(lines, "//")
		for ln := range strings.SplitSeq(body, "\n") {
			lines = append(lines, strings.TrimRight("// "+ln, " \t"))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func fieldComment(name, summary string) string {
	s := strings.Join(strings.Fields(summary), " ")
	if s == "" {
		return ""
	}
	return "// " + name + " " + lowerFirst(s) + ".\n"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")

	indent := -1
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n := len(ln) - len(strings.TrimLeft(ln, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent > 0 {
		for i, ln := range lines {
			if len(ln) >= indent {
				lines[i] = ln[indent:]
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
