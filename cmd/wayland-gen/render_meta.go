package main

import (
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
)

func metaVariableNames(c *corpus) map[*iface]string {
	metaVar := make(map[*iface]string, len(c.interfaces))
	used := make(map[string]bool, len(c.interfaces))
	for _, i := range c.interfaces {
		base := "interface" + i.GoName
		if i.Pkg.ImportPath != "" {
			base = "interface" + camel(packageName(i.Pkg.Tier)) + camel(packageName(i.Pkg.Dir)) + i.GoName
		}
		name := base
		for n := 2; used[name]; n++ {
			name = base + strconv.Itoa(n)
		}
		used[name] = true
		metaVar[i] = name
	}
	return metaVar
}

func renderRootMeta(c *corpus) ([]byte, error) {
	metaVar := metaVariableNames(c)
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("package wayland\n\n")
	for _, i := range c.interfaces {
		fmt.Fprintf(&b, "var %s = %s\n\n", metaVar[i], renderInterfaceLiteral(i, ""))
	}
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	b.WriteString("var generatedInterfaces = map[string]*Interface{\n")
	for _, name := range names {
		winner := c.byName[name][0].Winner
		fmt.Fprintf(&b, "\t%s: %s,\n", strconv.Quote(name), metaVar[winner])
	}
	b.WriteString("}\n\n")
	b.WriteString("var allInterfaces = []*Interface{\n")
	for _, i := range c.interfaces {
		fmt.Fprintf(&b, "\t%s,\n", metaVar[i])
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("gofmt generated source: %w", err)
	}
	return src, nil
}

func renderInterfaceLiteral(i *iface, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "&%sInterface{\n", prefix)
	fmt.Fprintf(&b, "\tName: %s,\n", strconv.Quote(i.Name))
	fmt.Fprintf(&b, "\tVersion: %d,\n", i.Version)
	if i.Frozen {
		b.WriteString("\tFrozen: true,\n")
	}
	if len(i.Requests) > 0 {
		fmt.Fprintf(&b, "\tRequests: []%sMessage{\n", prefix)
		for _, m := range i.Requests {
			fmt.Fprintf(&b, "\t\t%s,\n", renderMessageLiteral(m, prefix))
		}
		b.WriteString("\t},\n")
	}
	if len(i.Events) > 0 {
		fmt.Fprintf(&b, "\tEvents: []%sMessage{\n", prefix)
		for _, m := range i.Events {
			fmt.Fprintf(&b, "\t\t%s,\n", renderMessageLiteral(m, prefix))
		}
		b.WriteString("\t},\n")
	}
	if len(i.Enums) > 0 {
		fmt.Fprintf(&b, "\tEnums: []%sEnum{\n", prefix)
		for _, en := range i.Enums {
			fmt.Fprintf(&b, "\t\t%s,\n", renderEnumLiteral(en, prefix))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}")
	return b.String()
}

func renderMessageLiteral(m *msg, prefix string) string {
	var parts []string
	parts = append(parts, "Name: "+strconv.Quote(m.Name))
	parts = append(parts, fmt.Sprintf("Since: %d", m.Since))
	if m.DeprecatedSince != 0 {
		parts = append(parts, fmt.Sprintf("DeprecatedSince: %d", m.DeprecatedSince))
	}
	if m.Destructor {
		parts = append(parts, "Destructor: true")
	}
	if len(m.Args) > 0 {
		parts = append(parts, "Args: []"+prefix+"ArgSpec{"+renderArgsLiteral(m.Args, prefix)+"}")
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func renderArgsLiteral(args []*arg, prefix string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, renderArgLiteral(a, prefix))
	}
	return strings.Join(parts, ", ")
}

func renderArgLiteral(a *arg, prefix string) string {
	var parts []string
	parts = append(parts, "Name: "+strconv.Quote(a.Name))
	parts = append(parts, "Type: "+prefix+argTypeConst(a.Type))
	if a.InterfaceName != "" {
		parts = append(parts, "Interface: "+strconv.Quote(a.InterfaceName))
	}
	if a.EnumName != "" {
		parts = append(parts, "Enum: "+strconv.Quote(a.EnumName))
	}
	if a.AllowNull {
		parts = append(parts, "AllowNull: true")
	}
	if a.Since != 0 {
		parts = append(parts, fmt.Sprintf("Since: %d", a.Since))
	}
	if a.DeprecatedSince != 0 {
		parts = append(parts, fmt.Sprintf("DeprecatedSince: %d", a.DeprecatedSince))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func renderEnumLiteral(en *enum, prefix string) string {
	var parts []string
	parts = append(parts, "Name: "+strconv.Quote(en.Name))
	parts = append(parts, fmt.Sprintf("Since: %d", en.Since))
	if en.Bitfield {
		parts = append(parts, "Bitfield: true")
	}
	if len(en.Entries) > 0 {
		entries := make([]string, 0, len(en.Entries))
		for _, e := range en.Entries {
			var ep []string
			ep = append(ep, "Name: "+strconv.Quote(e.Name))
			ep = append(ep, "Value: "+literalInt64(e.Value))
			ep = append(ep, fmt.Sprintf("Since: %d", e.Since))
			if e.DeprecatedSince != 0 {
				ep = append(ep, fmt.Sprintf("DeprecatedSince: %d", e.DeprecatedSince))
			}
			entries = append(entries, "{"+strings.Join(ep, ", ")+"}")
		}
		parts = append(parts, "Entries: []"+prefix+"EnumEntry{"+strings.Join(entries, ", ")+"}")
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func argTypeConst(t string) string {
	switch t {
	case "int":
		return "ArgInt"
	case "uint":
		return "ArgUint"
	case "fixed":
		return "ArgFixed"
	case "string":
		return "ArgString"
	case "array":
		return "ArgArray"
	case "fd":
		return "ArgFD"
	case "new_id":
		return "ArgNewID"
	case "object":
		return "ArgObject"
	}
	return "ArgUint"
}

func literalInt64(v int64) string {
	if v >= 0 {
		return fmt.Sprintf("0x%x", v)
	}
	return strconv.FormatInt(v, 10)
}
