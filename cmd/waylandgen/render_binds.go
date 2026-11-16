package main

import "fmt"

func (r *renderCtx) renderDynamicBind(i *iface, m *msg) error {
	if !r.isRoot() || i.Name != "wl_registry" || m.Name != "bind" {
		return fmt.Errorf("dynamic bind template is only valid for root wl_registry.bind")
	}
	r.body.WriteString("func (r *Registry) Bind(g Global, version uint32) (*Proxy, error) {\n")
	r.body.WriteString("\tiface, ok := InterfaceByName(g.Interface)\n")
	r.body.WriteString("\tif !ok {\n\t\treturn nil, fmt.Errorf(\"bind %s: %w\", g.Interface, ErrInvalidArgument)\n\t}\n")
	r.body.WriteString("\tif version < 1 || version > g.Version || version > iface.Version {\n")
	r.body.WriteString("\t\treturn nil, fmt.Errorf(\"bind %s v%d: %w\", g.Interface, version, ErrVersion)\n\t}\n")
	r.body.WriteString("\tp, err := r.p.newObjectWithVersion(iface, version)\n")
	r.body.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	r.body.WriteString("\tif err := r.p.Send(0, Uint(g.Name), NewID(p.ID())); err != nil {\n")
	r.body.WriteString("\t\t_ = p.Release()\n\t\treturn nil, err\n\t}\n")
	r.body.WriteString("\treturn p, nil\n}\n\n")
	return nil
}

func (r *renderCtx) renderBind(i *iface) {
	hasHandler := i.HasEvents && !i.IsDisplay
	params := "r " + r.registryType() + ", g " + r.globalType()
	if hasHandler {
		params += ", h " + i.HandlerName
	}
	fmt.Fprintf(&r.body, "func Bind%s(%s) (*%s, error) {\n", i.GoName, params, i.GoName)
	if hasHandler {
		fmt.Fprintf(&r.body, "\tif h == nil {\n\t\treturn nil, fmt.Errorf(%q, %s)\n\t}\n", "bind "+i.GoName+": %w", r.errExpr("ErrInvalidArgument"))
	}
	fmt.Fprintf(&r.body, "\tv := min(g.Version, %s.Version)\n", r.metaExpr(i))
	r.body.WriteString("\tp, err := r.Bind(g, v)\n")
	r.body.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	if hasHandler {
		fmt.Fprintf(&r.body, "\treturn New%s(p, h), nil\n", i.GoName)
	} else {
		fmt.Fprintf(&r.body, "\treturn New%s(p), nil\n", i.GoName)
	}
	r.body.WriteString("}\n\n")
}

func (r *renderCtx) registryType() string {
	if r.isRoot() {
		return "*Registry"
	}
	return "*wayland.Registry"
}

func (r *renderCtx) globalType() string {
	if r.isRoot() {
		return "Global"
	}
	return "wayland.Global"
}
