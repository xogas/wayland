package main

import (
	"fmt"
	"strconv"
	"strings"
)

func (r *renderCtx) renderEnum(i *iface, en *enum) {
	typeName := i.GoName + en.GoName
	underlying := "int32"
	unsigned := en.Bitfield
	for _, e := range en.Entries {
		if e.Value > 1<<31-1 {
			unsigned = true
		}
	}
	if unsigned {
		underlying = "uint32"
	}
	fmt.Fprintf(&r.body, "type %s %s\n\n", typeName, underlying)
	if len(en.Entries) == 0 {
		return
	}
	r.body.WriteString("const (\n")
	for _, e := range en.Entries {
		fmt.Fprintf(&r.body, "\t%s%s %s = %s\n", typeName, e.GoName, typeName, literalInt64(e.Value))
	}
	r.body.WriteString(")\n\n")
}

func (r *renderCtx) renderType(i *iface) {
	fmt.Fprintf(&r.body, "type %s struct {\n\tp %s\n", i.GoName, r.proxyType())
	if i.HasEvents && !i.IsDisplay {
		fmt.Fprintf(&r.body, "\th %s\n", i.HandlerName)
	}
	r.body.WriteString("}\n\n")
}

func (r *renderCtx) renderProxy(i *iface) {
	recv := receiverName(i)
	fmt.Fprintf(&r.body, "func (%s *%s) Proxy() %s {\n\treturn %s.p\n}\n\n", recv, i.GoName, r.proxyType(), recv)
}

func (r *renderCtx) renderNew(i *iface) {
	if !i.HasEvents || i.IsDisplay {
		fmt.Fprintf(&r.body, "func New%s(p %s) *%s {\n\treturn &%s{p: p}\n}\n\n", i.GoName, r.proxyType(), i.GoName, i.GoName)
		return
	}
	fmt.Fprintf(&r.body, "func New%s(p %s, h %s) *%s {\n", i.GoName, r.proxyType(), i.HandlerName, i.GoName)
	fmt.Fprintf(&r.body, "\tobj := &%s{p: p, h: h}\n", i.GoName)
	r.body.WriteString("\tif h != nil {\n\t\t_ = p.SetSink(obj)\n\t}\n")
	if i.Name == "wl_callback" {
		r.body.WriteString("\tp.markServerOwned()\n")
	}
	r.body.WriteString("\treturn obj\n}\n\n")
}

func (r *renderCtx) renderHandler(i *iface) {
	if !i.HasEvents || i.IsDisplay {
		return
	}
	fmt.Fprintf(&r.body, "type %s interface {\n", i.HandlerName)
	if r.isRoot() && i.Name == "wl_registry" {
		r.body.WriteString("\tGlobal(g Global)\n")
		r.body.WriteString("\tGlobalRemove(name uint32)\n")
		r.body.WriteString("}\n\n")
		return
	}
	for _, e := range i.Events {
		fmt.Fprintf(&r.body, "\t%s(%s)\n", e.GoName, r.eventParams(e))
	}
	r.body.WriteString("}\n\n")
}

func (r *renderCtx) renderDispatch(i *iface) {
	if !i.HasEvents || i.IsDisplay {
		return
	}
	recv := receiverName(i)
	fmt.Fprintf(&r.body, "func (%s *%s) DispatchEvent(opcode uint16, args []%s) error {\n", recv, i.GoName, r.argType())
	r.body.WriteString("\tswitch opcode {\n")
	if r.isRoot() && i.Name == "wl_registry" {
		r.body.WriteString("\tcase 0:\n")
		fmt.Fprintf(&r.body, "\t\t%s.h.Global(Global{Name: args[0].Uint(), Interface: args[1].String(), Version: args[2].Uint()})\n", recv)
		r.body.WriteString("\tcase 1:\n")
		fmt.Fprintf(&r.body, "\t\t%s.h.GlobalRemove(args[0].Uint())\n", recv)
	} else {
		for n, e := range i.Events {
			exprs := make([]string, 0, len(e.Args))
			for index, a := range e.Args {
				exprs = append(exprs, eventExpr(a, index))
			}
			fmt.Fprintf(&r.body, "\tcase %d:\n\t\t%s.h.%s(%s)\n", n, recv, e.GoName, strings.Join(exprs, ", "))
		}
	}
	fmt.Fprintf(&r.body, "\tdefault:\n\t\treturn fmt.Errorf(%q, opcode, %s)\n", i.GoName+" event %d: %w", r.errExpr("ErrProtocolViolation"))
	r.body.WriteString("\t}\n\treturn nil\n}\n\n")
}

func (r *renderCtx) eventParams(e *msg) string {
	used := map[string]bool{"args": true, "opcode": true}
	var parts []string
	for _, a := range e.Args {
		name := uniqueName(goParamName(a.Name), used)
		parts = append(parts, name+" "+r.eventArgType(a))
	}
	return strings.Join(parts, ", ")
}

func (r *renderCtx) renderRequest(i *iface, m *msg) error {
	var newID *arg
	for _, a := range m.Args {
		if a.Type == "new_id" {
			newID = a
		}
	}
	var target *iface
	if newID != nil {
		if newID.Dynamic {
			return inputError(i.Protocol.XML, i.Name+"."+m.Name, fmt.Errorf("dynamic new_id must use the wl_registry.bind template"))
		}
		target = newID.Target
		if target == nil {
			return inputError(i.Protocol.XML, i.Name+"."+m.Name+"."+newID.Name, fmt.Errorf("unresolved new_id target"))
		}
	}
	recv := receiverName(i)
	used := map[string]bool{recv: true, "h": true, "p": true, "err": true, "obj": true, "int32": true, "uint32": true}
	for _, alias := range r.aliases {
		used[alias] = true
	}
	used["wayland"] = true
	used["fmt"] = true

	var params []requestParam
	for _, a := range m.Args {
		if a.Type == "new_id" {
			continue
		}
		typ, err := r.requestArgType(a)
		if err != nil {
			return inputError(i.Protocol.XML, i.Name+"."+m.Name+"."+a.Name, err)
		}
		params = append(params, requestParam{arg: a, name: uniqueName(goParamName(a.Name), used), typ: typ})
	}
	hasHandler := target != nil && target.HasEvents && !target.IsDisplay
	var sig []string
	for _, p := range params {
		sig = append(sig, p.name+" "+p.typ)
	}
	if hasHandler {
		sig = append(sig, "h "+r.handlerExpr(target))
	}
	ret := "error"
	if target != nil {
		ret = "(*" + r.typeExpr(target) + ", error)"
	}
	fmt.Fprintf(&r.body, "func (%s *%s) %s(%s) %s {\n", recv, i.GoName, m.GoName, strings.Join(sig, ", "), ret)
	if hasHandler {
		fmt.Fprintf(&r.body, "\tif h == nil {\n\t\treturn nil, fmt.Errorf(%q, %s)\n\t}\n", m.Name+": %w", r.errExpr("ErrInvalidArgument"))
	}
	for _, p := range params {
		if p.arg.Type != "object" || p.arg.AllowNull {
			continue
		}
		if target != nil {
			fmt.Fprintf(&r.body, "\tif %s == nil {\n\t\treturn nil, fmt.Errorf(%q, %s)\n\t}\n", p.name, m.Name+": nil "+p.arg.Name+": %w", r.errExpr("ErrInvalidArgument"))
		} else {
			fmt.Fprintf(&r.body, "\tif %s == nil {\n\t\treturn fmt.Errorf(%q, %s)\n\t}\n", p.name, m.Name+": nil "+p.arg.Name+": %w", r.errExpr("ErrInvalidArgument"))
		}
	}
	var nullIDs = make(map[*arg]string)
	for index, p := range params {
		if p.arg.Type != "object" || !p.arg.AllowNull {
			continue
		}
		local := uniqueName("objID"+strconv.Itoa(index), used)
		nullIDs[p.arg] = local
		fmt.Fprintf(&r.body, "\tvar %s uint32\n\tif %s != nil {\n\t\t%s = %s.Proxy().ID()\n\t}\n", local, p.name, local, p.name)
	}
	if target != nil {
		fmt.Fprintf(&r.body, "\tp, err := %s.p.NewObject(%s)\n", recv, r.metaExpr(target))
		r.body.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		if hasHandler {
			fmt.Fprintf(&r.body, "\tobj := %s(p, h)\n", r.newExpr(target))
		} else {
			fmt.Fprintf(&r.body, "\tobj := %s(p)\n", r.newExpr(target))
		}
	}
	args, err := r.requestArgs(m, params, nullIDs)
	if err != nil {
		return inputError(i.Protocol.XML, i.Name+"."+m.Name, err)
	}
	if target != nil {
		fmt.Fprintf(&r.body, "\tif err := %s.p.Send(%d, %s); err != nil {\n\t\t_ = p.Release()\n\t\treturn nil, err\n\t}\n", recv, m.Opcode, strings.Join(args, ", "))
		r.body.WriteString("\treturn obj, nil\n")
	} else {
		if len(args) == 0 {
			fmt.Fprintf(&r.body, "\treturn %s.p.Send(%d)\n", recv, m.Opcode)
		} else {
			fmt.Fprintf(&r.body, "\treturn %s.p.Send(%d, %s)\n", recv, m.Opcode, strings.Join(args, ", "))
		}
	}
	r.body.WriteString("}\n\n")
	return nil
}

type requestParam struct {
	arg  *arg
	name string
	typ  string
}

func (r *renderCtx) requestArgs(m *msg, params []requestParam, nullIDs map[*arg]string) ([]string, error) {
	byArg := make(map[*arg]requestParam, len(params))
	for _, p := range params {
		byArg[p.arg] = p
	}
	args := make([]string, 0, len(m.Args))
	for _, a := range m.Args {
		if a.Type == "new_id" {
			args = append(args, r.argCall("NewID")+"(p.ID())")
			continue
		}
		p, ok := byArg[a]
		if !ok {
			return nil, fmt.Errorf("arg %s is missing from the rendered signature", a.Name)
		}
		switch a.Type {
		case "int":
			if a.Enum != nil {
				args = append(args, r.argCall("Int")+"(int32("+p.name+"))")
			} else {
				args = append(args, r.argCall("Int")+"("+p.name+")")
			}
		case "uint":
			if a.Enum != nil {
				args = append(args, r.argCall("Uint")+"(uint32("+p.name+"))")
			} else {
				args = append(args, r.argCall("Uint")+"("+p.name+")")
			}
		case "fixed":
			args = append(args, r.argCall("Fixed")+"("+p.name+")")
		case "string":
			if a.AllowNull {
				args = append(args, r.argCall("String")+"("+p.name+")")
			} else {
				args = append(args, r.argCall("String")+"(&"+p.name+")")
			}
		case "array":
			args = append(args, r.argCall("Array")+"("+p.name+")")
		case "fd":
			args = append(args, r.argCall("FD")+"("+p.name+")")
		case "object":
			if a.AllowNull {
				args = append(args, r.argCall("Object")+"("+nullIDs[a]+")")
			} else {
				args = append(args, r.argCall("Object")+"("+p.name+".Proxy().ID())")
			}
		default:
			return nil, fmt.Errorf("arg %s: unsupported type %q", a.Name, a.Type)
		}
	}
	return args, nil
}
