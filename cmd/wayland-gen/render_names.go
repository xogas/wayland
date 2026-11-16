package main

import (
	"fmt"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

func (r *renderCtx) isRoot() bool { return r.pk.ImportPath == "" }

func (r *renderCtx) proxyType() string {
	if r.isRoot() {
		return "*Proxy"
	}
	return "*wayland.Proxy"
}

func (r *renderCtx) qual(p *pkg) string {
	if p == r.pk {
		return ""
	}
	if p.ImportPath == "" {
		return "wayland."
	}
	return r.aliases[p] + "."
}

func (r *renderCtx) typeExpr(t *iface) string {
	return r.qual(t.Pkg) + t.GoName
}

func (r *renderCtx) handlerExpr(t *iface) string {
	return r.qual(t.Pkg) + t.HandlerName
}

func (r *renderCtx) newExpr(t *iface) string {
	return r.qual(t.Pkg) + "New" + t.GoName
}

func (r *renderCtx) enumExpr(t *iface, en *enum) string {
	return r.qual(t.Pkg) + t.GoName + en.GoName
}

func (r *renderCtx) errExpr(name string) string {
	if r.isRoot() {
		return name
	}
	return "wayland." + name
}

func (r *renderCtx) metaExpr(t *iface) string {
	if !r.isRoot() && t.Pkg == r.pk && t.IsLoser {
		return "interface" + t.GoName
	}
	if r.isRoot() {
		return "MustInterface(" + strconv.Quote(t.Name) + ")"
	}
	return "wayland.MustInterface(" + strconv.Quote(t.Name) + ")"
}

func (r *renderCtx) argType() string {
	if r.isRoot() {
		return "Arg"
	}
	return "wayland.Arg"
}

func (r *renderCtx) argCall(fn string) string {
	if r.isRoot() {
		return fn
	}
	return "wayland." + fn
}

func (r *renderCtx) eventArgType(a *arg) string {
	switch a.Type {
	case "int":
		return "int32"
	case "uint":
		return "uint32"
	case "fixed":
		return "float64"
	case "string":
		if a.AllowNull {
			return "*string"
		}
		return "string"
	case "array":
		return "[]byte"
	case "fd":
		return "int"
	case "object", "new_id":
		return r.proxyType()
	}
	return "any"
}

func eventExpr(a *arg, index int) string {
	switch a.Type {
	case "int":
		return fmt.Sprintf("args[%d].Int()", index)
	case "uint":
		return fmt.Sprintf("args[%d].Uint()", index)
	case "fixed":
		return fmt.Sprintf("args[%d].Fixed()", index)
	case "string":
		if a.AllowNull {
			return fmt.Sprintf("args[%d].StringPtr()", index)
		}
		return fmt.Sprintf("args[%d].String()", index)
	case "array":
		return fmt.Sprintf("args[%d].Array()", index)
	case "fd":
		return fmt.Sprintf("args[%d].FD()", index)
	case "object", "new_id":
		return fmt.Sprintf("args[%d].Proxy()", index)
	}
	return "nil"
}

func (r *renderCtx) requestArgType(a *arg) (string, error) {
	if a.Enum != nil {
		if a.EnumTarget == nil {
			return "", fmt.Errorf("unresolved enum")
		}
		return r.enumExpr(a.EnumTarget, a.Enum), nil
	}
	switch a.Type {
	case "int":
		return "int32", nil
	case "uint":
		return "uint32", nil
	case "fixed":
		return "float64", nil
	case "string":
		if a.AllowNull {
			return "*string", nil
		}
		return "string", nil
	case "array":
		return "[]byte", nil
	case "fd":
		return "int", nil
	case "object":
		if a.Target == nil {
			return "", fmt.Errorf("object arg %s has no interface", a.Name)
		}
		return "*" + r.typeExpr(a.Target), nil
	}
	return "", fmt.Errorf("request arg type %q not implemented", a.Type)
}

func collectImports(pk *pkg) map[*pkg]bool {
	need := make(map[*pkg]bool)
	for _, i := range pk.Interfaces {
		for _, m := range i.Requests {
			for _, a := range m.Args {
				if a.Target != nil && a.Target.Pkg != pk && a.Target.Pkg.ImportPath != "" {
					need[a.Target.Pkg] = true
				}
				if a.EnumTarget != nil && a.EnumTarget.Pkg != pk && a.EnumTarget.Pkg.ImportPath != "" {
					need[a.EnumTarget.Pkg] = true
				}
			}
		}
	}
	return need
}

func assignAliases(pk *pkg, need map[*pkg]bool) map[*pkg]string {
	aliases := make(map[*pkg]string)
	seen := map[string]bool{"wayland": true, "fmt": true}
	var list []*pkg
	for p := range need {
		if p != pk {
			list = append(list, p)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ImportPath < list[j].ImportPath })
	for _, p := range list {
		cand := p.Name
		if seen[cand] || cand == "" {
			cand = packageName(p.Tier) + p.Name
		}
		if !token.IsIdentifier(cand) || token.IsKeyword(cand) {
			cand = "pkg" + p.Name
		}
		base := cand
		for n := 2; seen[cand] || !token.IsIdentifier(cand) || token.IsKeyword(cand); n++ {
			cand = base + strconv.Itoa(n)
		}
		seen[cand] = true
		aliases[p] = cand
	}
	return aliases
}

func receiverName(i *iface) string {
	name := lowerFirst(i.GoName)
	if name == "" || name == "_" || token.IsKeyword(name) || name == "p" || name == "err" || name == "obj" || name == "h" {
		return "receiver"
	}
	return name
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

var initialisms = map[string]string{
	"api":   "API",
	"fd":    "FD",
	"http":  "HTTP",
	"https": "HTTPS",
	"id":    "ID",
	"url":   "URL",
	"uuid":  "UUID",
}

func goParamName(s string) string {
	parts := strings.Split(strings.ReplaceAll(s, "-", "_"), "_")
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(part)
			continue
		}
		if up, ok := initialisms[part]; ok {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	name := b.String()
	if name == "" {
		return "arg"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "arg" + name
	}
	if goKeywords[name] {
		name += "_"
	}
	return name
}

func uniqueName(base string, used map[string]bool) string {
	name := base
	if name == "" {
		name = "arg"
	}
	for used[name] {
		name += "_"
	}
	used[name] = true
	return name
}
