package main

import (
	"fmt"
	"strings"
)

// tplData is the root template context for one generated interface file.
type tplData struct {
	Package  string
	TypeName string
	IfName   string
	Version  int
	Imports  string
	HasEnums bool

	Enums    []tplEnum
	Requests []tplReq
	Events   []tplEv

	EventFDCounts map[uint16]int
	HasFDEvent    bool

	IsRoot     bool   // true when generating for the wayland package itself
	WaylandPkg string // "wayland." for protocol sub-packages, "" for root
}

type tplReq struct {
	Name          string
	OpName        string
	StructName    string
	Opcode        int
	Since         int
	Args          []tplArg
	HasNewID      bool   // new_id arg whose interface is in this protocol
	NewIDType     string // Go type created by this request
	HasCrossNewID bool   // new_id arg without resolvable interface
	MethodArgs    string // pre-computed method signature (new_id args filtered)
}

type tplEv struct {
	Name       string
	OpName     string
	StructName string
	FuncName   string
	Opcode     int
	Since      int
	Args       []tplArg
}

type tplArg struct {
	GoName    string
	GoType    string
	ParamName string
	WireRead  string
	WriteFn   string
	IsNewID   bool
}

type tplEnum struct {
	Name    string
	Type    string
	Entries []tplEntry
}

type tplEntry struct {
	Const string
	Val   string
}

// buildData assembles the template context for one interface.
func buildData(iface *Interface, typeName string, cfg GenerateConfig, knownIface map[string]bool) (*tplData, error) {
	isRoot := cfg.Package == "wayland"
	d := &tplData{
		Package:  cfg.Package,
		TypeName: typeName,
		IfName:   iface.Name,
		Version:  iface.Version,
		IsRoot:   isRoot,
	}
	if !isRoot {
		d.WaylandPkg = "wayland."
	}

	d.Enums, d.HasEnums = buildEnums(iface, typeName)

	var err error
	d.Requests, err = buildRequests(iface, typeName, cfg, knownIface)
	if err != nil {
		return nil, fmt.Errorf("requests: %w", err)
	}

	d.Events, d.EventFDCounts, d.HasFDEvent, err = buildEvents(iface, typeName)
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	d.Imports = buildImports(len(d.Requests) > 0 || len(d.Events) > 0, isRoot)

	return d, nil
}

func buildEnums(iface *Interface, typeName string) ([]tplEnum, bool) {
	var out []tplEnum
	for i := range iface.Enums {
		e := &iface.Enums[i]
		en := tplEnum{
			Name: pascal(e.Name),
			Type: typeName + pascal(e.Name),
		}
		for j := range e.Entries {
			en.Entries = append(en.Entries, tplEntry{
				Const: en.Type + pascal(e.Entries[j].Name),
				Val:   fmt.Sprintf("%d", e.Entries[j].Value),
			})
		}
		out = append(out, en)
	}
	return out, len(out) > 0
}

func buildRequests(iface *Interface, tn string, cfg GenerateConfig, knownIface map[string]bool) ([]tplReq, error) {
	var out []tplReq
	for opcode := range iface.Requests {
		r := &iface.Requests[opcode]
		reqName := pascal(r.Name)
		rd := tplReq{
			Name:       reqName,
			OpName:     tn + "Request" + reqName,
			StructName: tn + reqName + "Request",
			Opcode:     opcode,
			Since:      max(r.Since, 1),
		}

		for j := range r.Args {
			arg, err := buildArg(&r.Args[j])
			if err != nil {
				return nil, err
			}
			rd.Args = append(rd.Args, arg)
			if !arg.IsNewID {
				continue
			}
			if ifn := r.Args[j].Interface; ifn != "" && knownIface[ifn] {
				rd.HasNewID = true
				rd.NewIDType = typeName(ifn, cfg.TrimPrefix, "")
			} else {
				rd.HasCrossNewID = true
			}
		}
		rd.MethodArgs = methodArgs(rd)
		out = append(out, rd)
	}
	return out, nil
}

func buildEvents(iface *Interface, typeName string) (events []tplEv, fdCounts map[uint16]int, hasFD bool, err error) {
	for opcode := range iface.Events {
		e := &iface.Events[opcode]
		evtName := pascal(e.Name)
		ed := tplEv{
			Name:       evtName,
			OpName:     typeName + "Event" + evtName,
			StructName: typeName + evtName + "Event",
			FuncName:   typeName + evtName + "Func",
			Opcode:     opcode,
			Since:      max(e.Since, 1),
		}
		fdCount := 0
		for j := range e.Args {
			arg, err := buildArg(&e.Args[j])
			if err != nil {
				return nil, nil, false, err
			}
			ed.Args = append(ed.Args, arg)
			if e.Args[j].Type == "fd" {
				fdCount++
			}
		}
		if fdCount > 0 {
			if fdCounts == nil {
				fdCounts = make(map[uint16]int)
			}
			fdCounts[uint16(opcode)] = fdCount
		}
		events = append(events, ed)
	}
	return events, fdCounts, len(fdCounts) > 0, nil
}

// buildArg maps a wire type to its Go type and codec calls.
func buildArg(a *Arg) (tplArg, error) {
	ad := tplArg{GoName: pascal(a.Name), ParamName: camel(a.Name)}
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
		return tplArg{}, fmt.Errorf("unknown arg type %q for arg %q", a.Type, a.Name)
	}
	return ad, nil
}

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

// methodArgs renders the wrapper method parameter list, dropping new_id args
// (the proxy is allocated inside the generated method).
func methodArgs(r tplReq) string {
	var parts []string
	for _, a := range r.Args {
		if (r.HasNewID || r.HasCrossNewID) && a.IsNewID {
			continue
		}
		parts = append(parts, a.ParamName+" "+a.GoType)
	}
	return strings.Join(parts, ", ")
}

// injectSyntheticArgs adds hidden interface/version args for new_id arguments
// that lack an explicit interface attribute (e.g. wl_registry.bind).
func injectSyntheticArgs(iface *Interface) {
	for i := range iface.Requests {
		req := &iface.Requests[i]
		for j := range req.Args {
			if req.Args[j].Type == "new_id" && req.Args[j].Interface == "" {
				synthetic := []Arg{
					{Name: "interface", Type: "string"},
					{Name: "version", Type: "uint"},
				}
				newArgs := make([]Arg, 0, len(req.Args)+2)
				newArgs = append(newArgs, req.Args[:j]...)
				newArgs = append(newArgs, synthetic...)
				newArgs = append(newArgs, req.Args[j:]...)
				req.Args = newArgs
				break
			}
		}
	}
}
