package main

import (
	"fmt"
	"strconv"
	"strings"
)

type protocol struct {
	Name       string
	Interfaces []*iface
}

type iface struct {
	Name        string
	Version     uint32
	Requests    []*msg
	Events      []*msg
	Enums       []*enum
	GoName      string
	HandlerName string
	HasEvents   bool
	IsDisplay   bool
}

type msg struct {
	Name       string
	Since      uint32
	Args       []*arg
	GoName     string
	Destructor bool
}

type arg struct {
	Name      string
	Type      string
	Interface string
	Enum      string
	AllowNull bool
}

type enum struct {
	Name     string
	Since    uint32
	Bitfield bool
	Entries  []*entry
}

type entry struct {
	Name  string
	Value int64
	Since uint32
}

func build(p *xmlProtocol) (*protocol, error) {
	out := &protocol{Name: p.Name}
	names := make([]string, 0, len(p.Interfaces))
	for _, x := range p.Interfaces {
		names = append(names, x.Name)
	}
	prefix := commonPrefix(names)
	for _, x := range p.Interfaces {
		iface, err := buildInterface(x, prefix)
		if err != nil {
			return nil, err
		}
		out.Interfaces = append(out.Interfaces, iface)
	}
	byName := make(map[string]*iface, len(out.Interfaces))
	for _, i := range out.Interfaces {
		if _, exists := byName[i.Name]; exists {
			return nil, fmt.Errorf("duplicate interface %s", i.Name)
		}
		byName[i.Name] = i
	}
	for _, i := range out.Interfaces {
		for _, m := range append(append([]*msg{}, i.Requests...), i.Events...) {
			for _, a := range m.Args {
				if a.Type == "new_id" && a.Interface != "" {
					if byName[a.Interface] == nil {
						return nil, fmt.Errorf("%s.%s: unknown new_id interface %q", i.Name, m.Name, a.Interface)
					}
				}
				if a.Type == "object" && a.Interface != "" {
					if byName[a.Interface] == nil {
						return nil, fmt.Errorf("%s.%s: unknown object interface %q", i.Name, m.Name, a.Interface)
					}
				}
			}
		}
	}
	return out, nil
}

func buildInterface(x xmlInterface, prefix string) (*iface, error) {
	if x.Name == "" {
		return nil, fmt.Errorf("interface: empty name")
	}
	version, err := parseUint(x.Version, 0, x.Name+" version")
	if err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, fmt.Errorf("%s: invalid version %d", x.Name, version)
	}
	short := strings.TrimPrefix(x.Name, prefix)
	if short == "" || short == x.Name && prefix != "" {
		return nil, fmt.Errorf("%s: cannot strip prefix %q", x.Name, prefix)
	}
	i := &iface{
		Name:        x.Name,
		Version:     version,
		GoName:      camel(short),
		HandlerName: camel(short) + "Handler",
		IsDisplay:   x.Name == "wl_display",
	}
	if i.GoName == "" {
		return nil, fmt.Errorf("%s: empty Go name", x.Name)
	}
	for _, r := range x.Requests {
		m, err := buildMessage(r, version, "request", x.Name)
		if err != nil {
			return nil, err
		}
		i.Requests = append(i.Requests, m)
	}
	for _, e := range x.Events {
		m, err := buildMessage(e, version, "event", x.Name)
		if err != nil {
			return nil, err
		}
		i.Events = append(i.Events, m)
	}
	i.HasEvents = len(i.Events) > 0
	for _, e := range x.Enums {
		en, err := buildEnum(e, version, x.Name)
		if err != nil {
			return nil, err
		}
		i.Enums = append(i.Enums, en)
	}
	return i, nil
}

func buildMessage(x xmlMessage, version uint32, kind, ifaceName string) (*msg, error) {
	if x.Name == "" {
		return nil, fmt.Errorf("%s: %s with empty name", ifaceName, kind)
	}
	since, err := parseUint(x.Since, 1, ifaceName+"."+x.Name+" since")
	if err != nil {
		return nil, err
	}
	if since < 1 || since > version {
		return nil, fmt.Errorf("%s.%s: since %d outside version %d", ifaceName, x.Name, since, version)
	}
	m := &msg{
		Name:   x.Name,
		Since:  since,
		GoName: camel(x.Name),
	}
	m.Destructor = x.Type == "destructor"
	for _, a := range x.Args {
		aa, err := buildArg(a, ifaceName, x.Name)
		if err != nil {
			return nil, err
		}
		m.Args = append(m.Args, aa)
	}
	return m, nil
}

func buildArg(x xmlArg, ifaceName, msgName string) (*arg, error) {
	if x.Name == "" {
		return nil, fmt.Errorf("%s.%s: arg with empty name", ifaceName, msgName)
	}
	switch x.Type {
	case "int", "uint", "fixed", "string", "array", "fd", "new_id", "object":
	default:
		return nil, fmt.Errorf("%s.%s.%s: unsupported type %q", ifaceName, msgName, x.Name, x.Type)
	}
	allow := x.AllowNull == "true" || x.AllowNull == "1"
	if allow && x.Type != "string" && x.Type != "array" && x.Type != "new_id" && x.Type != "object" {
		return nil, fmt.Errorf("%s.%s.%s: allow-null on %s", ifaceName, msgName, x.Name, x.Type)
	}
	if x.Type == "new_id" && x.Interface == "" && !(ifaceName == "wl_registry" && msgName == "bind") {
		return nil, fmt.Errorf("%s.%s.%s: dynamic new_id only allowed on wl_registry.bind", ifaceName, msgName, x.Name)
	}
	return &arg{
		Name:      x.Name,
		Type:      x.Type,
		Interface: x.Interface,
		Enum:      x.Enum,
		AllowNull: allow,
	}, nil
}

func buildEnum(x xmlEnum, version uint32, ifaceName string) (*enum, error) {
	if x.Name == "" {
		return nil, fmt.Errorf("%s: enum with empty name", ifaceName)
	}
	since, err := parseUint(x.Since, 1, ifaceName+"."+x.Name+" since")
	if err != nil {
		return nil, err
	}
	if since < 1 || since > version {
		return nil, fmt.Errorf("%s.%s: since %d outside version %d", ifaceName, x.Name, since, version)
	}
	e := &enum{Name: x.Name, Since: since, Bitfield: x.Bitfield == "true"}
	for _, en := range x.Entries {
		v, err := strconv.ParseInt(en.Value, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("%s.%s.%s: value %q: %w", ifaceName, x.Name, en.Name, en.Value, err)
		}
		es, err := parseUint(en.Since, 1, ifaceName+"."+x.Name+"."+en.Name+" since")
		if err != nil {
			return nil, err
		}
		e.Entries = append(e.Entries, &entry{Name: en.Name, Value: v, Since: es})
	}
	return e, nil
}

func parseUint(s string, def uint32, what string) (uint32, error) {
	if s == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", what, s, err)
	}
	return uint32(v), nil
}

func commonPrefix(names []string) string {
	if len(names) < 2 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	if i := strings.LastIndex(prefix, "_"); i >= 0 {
		return prefix[:i+1]
	}
	return ""
}

func camel(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
