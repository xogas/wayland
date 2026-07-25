package main

import (
	"fmt"
	"strings"
)

// GoInterface is the Go-specific view of a single protocol Interface.
// Field names match what text/template expects for rendering.
type GoInterface struct {
	Package  string // Go package name
	TypeName string // PascalCase Go type name, e.g. "Display"
	IfName   string // protocol interface name, e.g. "wl_display"
	Version  int
	Imports  string // import block as raw string

	Enums    []GoEnum
	Requests []GoRequest
	Events   []GoEvent

	EventFDCounts map[uint16]int
	HasFDEvent    bool
	WaylandPkg    string // "wayland." for sub-pkgs, "" for root
}

// GoRequest is the Go-specific view of a protocol Request.
type GoRequest struct {
	Name          string
	OpName        string
	StructName    string
	Opcode        int
	Since         int
	Args          []GoArg
	HasNewID      bool   // new_id with resolvable interface in same protocol
	NewIDType     string // Go type created by this request
	HasCrossNewID bool   // new_id without resolvable interface
	MethodArgs    string // pre-computed method signature (new_id args filtered)
	IsDestructor  bool
}

// GoEvent is the Go-specific view of a protocol Event.
type GoEvent struct {
	Name       string
	OpName     string
	StructName string
	FuncName   string
	Opcode     int
	Since      int
	Args       []GoArg
}

// GoArg is the Go-specific view of an argument.
type GoArg struct {
	GoName    string
	GoType    string
	ParamName string
	WireRead  string
	WriteFn   string
	IsNewID   bool
}

// GoEnum is the Go-specific view of an enum.
type GoEnum struct {
	Name    string
	Type    string
	Entries []GoEnumEntry
}

// GoEnumEntry is the Go-specific view of an enum entry.
type GoEnumEntry struct {
	Const string
	Val   string
}

// convertInterface converts a single Interface into its Go-specific view.
func convertInterface(iface *Interface, pkg, prefix string, knownIface map[string]bool) GoInterface {
	tn := typeName(iface.Name, prefix)

	isRoot := pkg == "wayland"
	g := GoInterface{
		Package:  pkg,
		TypeName: tn,
		IfName:   iface.Name,
		Version:  iface.Version,
	}
	if !isRoot {
		g.WaylandPkg = "wayland."
	}

	g.Enums = buildEnums(iface, tn)
	g.Requests = buildRequests(iface, tn, prefix, knownIface)
	g.Events, g.EventFDCounts, g.HasFDEvent = buildEvents(iface, tn)

	hasWire := len(g.Requests) > 0 || len(g.Events) > 0
	g.Imports = buildImports(hasWire, isRoot)

	return g
}

// buildEnums converts Interface enums to GoEnums.
func buildEnums(iface *Interface, typeName string) []GoEnum {
	var out []GoEnum
	for i := range iface.Enums {
		e := &iface.Enums[i]
		en := GoEnum{
			Name: pascal(e.Name),
			Type: typeName + pascal(e.Name),
		}
		for j := range e.Entries {
			en.Entries = append(en.Entries, GoEnumEntry{
				Const: en.Type + pascal(e.Entries[j].Name),
				Val:   fmt.Sprintf("%d", e.Entries[j].Value),
			})
		}
		out = append(out, en)
	}
	return out
}

// buildRequests converts Interface requests to GoRequests.
func buildRequests(iface *Interface, tn, prefix string, knownIface map[string]bool) []GoRequest {
	var out []GoRequest
	for opcode := range iface.Requests {
		r := &iface.Requests[opcode]
		reqName := pascal(r.Name)
		rd := GoRequest{
			Name:         reqName,
			OpName:       tn + "Request" + reqName,
			StructName:   tn + reqName + "Request",
			Opcode:       opcode,
			Since:        max(r.Since, 1),
			IsDestructor: r.Type == "destructor",
		}

		for j := range r.Args {
			ga := buildArg(&r.Args[j])
			// Synthetic args: new_id without interface attribute
			// (e.g. wl_registry.bind) needs interface/version injected.
			if ga.IsNewID && r.Args[j].Interface == "" {
				rd.Args = append(rd.Args,
					GoArg{GoName: "Interface", ParamName: "interface_", GoType: "string", WireRead: "r.String()", WriteFn: "String"},
					GoArg{GoName: "Version", ParamName: "version", GoType: "uint32", WireRead: "r.Uint32()", WriteFn: "Uint32"},
				)
			}
			rd.Args = append(rd.Args, ga)

			if !ga.IsNewID {
				continue
			}
			if ifn := r.Args[j].Interface; ifn != "" && knownIface[ifn] {
				rd.HasNewID = true
				rd.NewIDType = typeName(ifn, prefix)
			} else {
				rd.HasCrossNewID = true
			}
		}
		rd.MethodArgs = methodArgs(rd)
		out = append(out, rd)
	}
	return out
}

// buildEvents converts Interface events to GoEvents and returns fd count info.
func buildEvents(iface *Interface, tn string) ([]GoEvent, map[uint16]int, bool) {
	var events []GoEvent
	var fdCounts map[uint16]int
	hasFD := false
	for opcode := range iface.Events {
		e := &iface.Events[opcode]
		evtName := pascal(e.Name)
		ed := GoEvent{
			Name:       evtName,
			OpName:     tn + "Event" + evtName,
			StructName: tn + evtName + "Event",
			FuncName:   tn + evtName + "Func",
			Opcode:     opcode,
			Since:      max(e.Since, 1),
		}
		fdCount := 0
		for j := range e.Args {
			ga := buildArg(&e.Args[j])
			ed.Args = append(ed.Args, ga)
			if e.Args[j].Type == "fd" {
				fdCount++
			}
		}
		if fdCount > 0 {
			if fdCounts == nil {
				fdCounts = make(map[uint16]int)
			}
			fdCounts[uint16(opcode)] = fdCount
			hasFD = true
		}
		events = append(events, ed)
	}
	return events, fdCounts, hasFD
}

// buildArg maps a parsed Arg to its Go-specific view.
func buildArg(a *Arg) GoArg {
	ad := GoArg{GoName: pascal(a.Name), ParamName: camel(a.Name)}
	switch a.Type {
	case "int":
		ad.GoType, ad.WireRead, ad.WriteFn = "int32", "r.Int32()", "Int32"
	case "uint":
		ad.GoType, ad.WireRead, ad.WriteFn = "uint32", "r.Uint32()", "Uint32"
	case "fixed":
		ad.GoType, ad.WireRead, ad.WriteFn = "wire.Fixed", "r.Fixed()", "Fixed"
	case "string":
		ad.GoType, ad.WireRead, ad.WriteFn = "string", "r.String()", "String"
	case "object":
		ad.GoType, ad.WireRead, ad.WriteFn = "wire.ObjectID", "r.Object()", "Object"
	case "new_id":
		ad.GoType, ad.WireRead, ad.WriteFn, ad.IsNewID = "wire.NewID", "r.NewID()", "NewID", true
	case "array":
		ad.GoType, ad.WireRead, ad.WriteFn = "[]byte", "r.Array()", "Array"
	case "fd":
		ad.GoType, ad.WireRead, ad.WriteFn = "int", "r.Fd()", "Fd"
	default:
		ad.GoType, ad.WireRead, ad.WriteFn = "??", "??", "??"
	}
	return ad
}

// methodArgs renders the wrapper method parameter list, dropping new_id args
// (the proxy is allocated inside the generated method).
func methodArgs(r GoRequest) string {
	var parts []string
	for _, a := range r.Args {
		if (r.HasNewID || r.HasCrossNewID) && a.IsNewID {
			continue
		}
		parts = append(parts, a.ParamName+" "+a.GoType)
	}
	return joinArgsStr(parts)
}

func joinArgsStr(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(parts[0])
	for _, p := range parts[1:] {
		_, _ = result.WriteString(", " + p)
	}
	return result.String()
}

// buildImports returns the raw import block for a generated file.
func buildImports(hasWire bool, isRoot bool) string {
	var imps []string
	if hasWire {
		imps = append(imps, `"github.com/xogas/wayland/wire"`)
	}
	if !isRoot {
		imps = append(imps, `"github.com/xogas/wayland"`)
	}
	if len(imps) == 0 {
		return ""
	}
	return "\n" + strings.Join(imps, "\n") + "\n"
}
