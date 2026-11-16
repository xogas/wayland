package main

import (
	"sort"
	"strconv"
	"strings"
)

const modulePath = "github.com/xogas/wayland"

func buildCorpus(parsed []parsedSource) *corpus {
	c := &corpus{byName: make(map[string][]*iface)}
	pkgsByKey := make(map[string]*pkg)
	order := make([]*pkg, 0)
	protocols := make([]*protocol, 0, len(parsed))

	for _, ps := range parsed {
		pk := packageFor(ps.Source, pkgsByKey)
		if !containsPkg(order, pk) {
			order = append(order, pk)
		}
		proto := buildProtocol(ps, pk)
		pk.Protocols = append(pk.Protocols, proto)
		protocols = append(protocols, proto)
	}

	// Root first, then protocol paths. This is the metadata ordering.
	sort.SliceStable(order, func(i, j int) bool {
		ri := order[i].ImportPath == ""
		rj := order[j].ImportPath == ""
		if ri != rj {
			return ri
		}
		if ri {
			return false
		}
		return order[i].ImportPath < order[j].ImportPath
	})
	sort.SliceStable(protocols, func(i, j int) bool {
		ri := protocols[i].Pkg.ImportPath == ""
		rj := protocols[j].Pkg.ImportPath == ""
		if ri != rj {
			return ri
		}
		if ri {
			return false
		}
		return protocols[i].Rel < protocols[j].Rel
	})
	c.pkgs = order
	for _, pk := range order {
		if pk.ImportPath == "" {
			c.root = pk
		}
	}
	c.protocols = protocols

	for _, pk := range c.pkgs {
		assignPackageNames(pk)
		pk.byName = make(map[string]*iface, len(pk.Interfaces))
		for _, i := range pk.Interfaces {
			pk.byName[i.Name] = i
		}
	}
	for _, proto := range c.protocols {
		c.interfaces = append(c.interfaces, proto.Interfaces...)
	}
	for _, i := range c.interfaces {
		c.byName[i.Name] = append(c.byName[i.Name], i)
	}
	resolveDuplicates(c)
	for _, i := range c.interfaces {
		if i.IsLoser {
			i.Pkg.LocalMeta = append(i.Pkg.LocalMeta, i)
		}
	}
	for _, pk := range c.pkgs {
		resolvePackage(c, pk)
	}
	return c
}

func containsPkg(pkgs []*pkg, want *pkg) bool {
	for _, p := range pkgs {
		if p == want {
			return true
		}
	}
	return false
}

func packageFor(src sourceFile, seen map[string]*pkg) *pkg {
	if src.Rel == "wayland.xml" {
		if p := seen[""]; p != nil {
			return p
		}
		p := &pkg{
			Name:    "wayland",
			OutFile: "wayland_gen.go",
		}
		seen[""] = p
		return p
	}
	parts := strings.Split(src.Rel, "/")
	tier, dir := parts[0], parts[1]
	key := tier + "/" + dir
	if p := seen[key]; p != nil {
		return p
	}
	file := strings.ReplaceAll(dir, "-", "_") + "_gen.go"
	p := &pkg{
		Tier:       tier,
		Dir:        dir,
		ImportPath: modulePath + "/protocols/" + tier + "/" + dir,
		Name:       packageName(dir),
		OutFile:    "protocols/" + tier + "/" + dir + "/" + file,
	}
	seen[key] = p
	return p
}

func buildProtocol(ps parsedSource, pk *pkg) *protocol {
	xp := ps.XML
	proto := &protocol{
		Name: xp.Name,
		XML:  ps.Source.Path,
		Rel:  ps.Source.Rel,
		Pkg:  pk,
	}
	if pk.ImportPath != "" {
		parts := strings.Split(ps.Source.Rel, "/")
		proto.Tier, proto.Dir = parts[0], parts[1]
	}
	names := make([]string, 0, len(xp.Interfaces))
	for _, xi := range xp.Interfaces {
		names = append(names, xi.Name)
	}
	prefix := commonPrefix(names)
	if pk.ImportPath == "" {
		prefix = "wl_"
	}
	for _, xi := range xp.Interfaces {
		i := buildInterface(xi, prefix, proto)
		proto.Interfaces = append(proto.Interfaces, i)
		pk.Interfaces = append(pk.Interfaces, i)
	}
	return proto
}

func buildInterface(x xmlInterface, prefix string, proto *protocol) *iface {
	version := parseUint32(x.Version, 0)
	frozen := parseBool(x.Frozen)
	goName := camel(strings.TrimPrefix(x.Name, prefix))
	i := &iface{
		Name:        x.Name,
		Version:     version,
		Frozen:      frozen,
		ShortGoName: goName,
		GoName:      goName,
		Protocol:    proto,
		Pkg:         proto.Pkg,
		IsDisplay:   x.Name == "wl_display",
	}
	for _, r := range x.Requests {
		m := buildMessage(r, true)
		m.Opcode = uint16(len(i.Requests))
		i.Requests = append(i.Requests, m)
	}
	for _, e := range x.Events {
		m := buildMessage(e, false)
		m.Opcode = uint16(len(i.Events))
		i.Events = append(i.Events, m)
	}
	i.HasEvents = len(i.Events) > 0
	for _, e := range x.Enums {
		i.Enums = append(i.Enums, buildEnum(e))
	}
	return i
}

func buildMessage(x xmlMessage, request bool) *msg {
	m := &msg{
		Name:            x.Name,
		Since:           parseSince(x.Since),
		DeprecatedSince: parseDeprecated(x.DeprecatedSince),
		Destructor:      x.Type == "destructor",
		GoName:          camel(x.Name),
		IsRequest:       request,
	}
	for _, a := range x.Args {
		m.Args = append(m.Args, buildArg(a))
	}
	return m
}

func buildArg(x xmlArg) *arg {
	return &arg{
		Name:          x.Name,
		Type:          x.Type,
		InterfaceName: x.Interface,
		EnumName:      x.Enum,
		AllowNull:     parseBool(x.AllowNull),
	}
}

func buildEnum(x xmlEnum) *enum {
	en := &enum{
		Name:     x.Name,
		Since:    parseSince(x.Since),
		Bitfield: parseBool(x.Bitfield),
		GoName:   camel(x.Name),
	}
	for _, xe := range x.Entries {
		v, _ := strconv.ParseInt(xe.Value, 0, 64)
		en.Entries = append(en.Entries, &entry{
			Name:            xe.Name,
			Value:           v,
			Since:           parseSince(xe.Since),
			DeprecatedSince: parseDeprecated(xe.DeprecatedSince),
			GoName:          camel(xe.Name),
		})
	}
	return en
}

func parseUint32(s string, def uint32) uint32 {
	if s == "" {
		return def
	}
	v, _ := strconv.ParseUint(s, 0, 32)
	return uint32(v)
}

func parseSince(s string) uint32 {
	return parseUint32(s, 1)
}

func parseDeprecated(s string) uint32 {
	return parseUint32(s, 0)
}

func parseBool(s string) bool {
	return s == "true" || s == "1"
}

func assignPackageNames(pk *pkg) {
	for _, i := range pk.Interfaces {
		i.GoName = i.ShortGoName
		i.HandlerName = i.GoName + "Handler"
	}
	seen := make(map[string]bool, len(pk.Interfaces))
	for _, i := range pk.Interfaces {
		if seen[i.GoName] {
			for _, i := range pk.Interfaces {
				i.GoName = camel(i.Name)
				i.HandlerName = i.GoName + "Handler"
			}
			return
		}
		seen[i.GoName] = true
	}
}

func resolveDuplicates(c *corpus) {
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		defs := c.byName[name]
		for _, d := range defs {
			d.Winner = d
		}
		if len(defs) == 1 {
			continue
		}
		var stable []*iface
		for _, d := range defs {
			if d.Protocol.Tier == "stable" {
				stable = append(stable, d)
			}
		}
		winner := stable[0]
		for _, d := range defs {
			d.Winner = winner
			if d != winner {
				d.IsLoser = true
			}
		}
	}
}

func resolvePackage(c *corpus, pk *pkg) {
	for _, i := range pk.Interfaces {
		for _, m := range i.Requests {
			for _, a := range m.Args {
				resolveArg(c, i, m, a)
			}
		}
		for _, m := range i.Events {
			for _, a := range m.Args {
				resolveArg(c, i, m, a)
			}
		}
	}
}

func resolveArg(c *corpus, i *iface, m *msg, a *arg) {
	if a.InterfaceName != "" {
		a.Target = resolveInterface(c, i, a.InterfaceName)
	} else if a.Type == "new_id" && i.Name == "wl_registry" && m.Name == "bind" && i.Pkg.ImportPath == "" {
		a.Dynamic = true
	}
	if a.EnumName != "" {
		enumIface := i
		enumName := a.EnumName
		if dot := strings.LastIndex(enumName, "."); dot >= 0 {
			enumIface = resolveInterface(c, i, enumName[:dot])
			enumName = enumName[dot+1:]
		}
		a.EnumTarget = enumIface
		a.Enum = findEnum(enumIface, enumName)
	}
}

func resolveInterface(c *corpus, src *iface, name string) *iface {
	if local, ok := src.Pkg.byName[name]; ok {
		return local
	}
	return c.byName[name][0].Winner
}

func findEnum(i *iface, name string) *enum {
	for _, en := range i.Enums {
		if en.Name == name {
			return en
		}
	}
	return nil
}
