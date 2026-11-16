package main

import (
	"fmt"
	"go/token"
	"sort"
	"strings"
)

func assignPackageNames(pk *pkg) error {
	fallback := false
	for {
		seen := make(map[string]*iface, len(pk.Interfaces))
		for _, i := range pk.Interfaces {
			if fallback {
				i.GoName = camel(i.Name)
			} else {
				i.GoName = i.ShortGoName
			}
			i.HandlerName = i.GoName + "Handler"
			if !token.IsIdentifier(i.GoName) || !token.IsExported(i.GoName) {
				return inputError(i.Protocol.XML, i.Name, fmt.Errorf("invalid Go type name %q", i.GoName))
			}
			if prev, ok := seen[i.GoName]; ok {
				if fallback {
					return inputError(i.Protocol.XML, i.Name, fmt.Errorf("go type name %q collides with %s after full-name fallback", i.GoName, prev.Protocol.XML))
				}
				fallback = true
				break
			}
			seen[i.GoName] = i
		}
		if !fallback || len(seen) == len(pk.Interfaces) {
			return nil
		}
	}
}

func resolveDuplicates(c *corpus) error {
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
		if len(stable) != 1 {
			files := make([]string, 0, len(defs))
			for _, d := range defs {
				files = append(files, d.Protocol.XML)
			}
			return inputError("", name, fmt.Errorf("ambiguous duplicate interface in %s: no single stable definition", strings.Join(files, ", ")))
		}
		winner := stable[0]
		for _, d := range defs {
			d.Winner = winner
			if d != winner {
				d.IsLoser = true
			}
		}
	}
	return nil
}

func validateIdentifiers(pk *pkg) error {
	seen := make(map[string]string)
	add := func(id, what string, i *iface) error {
		if !token.IsIdentifier(id) {
			return inputError(i.Protocol.XML, i.Name, fmt.Errorf("%s: invalid Go identifier %q", what, id))
		}
		if prev, ok := seen[id]; ok {
			return inputError(i.Protocol.XML, i.Name, fmt.Errorf("%s %q collides with %s", what, id, prev))
		}
		seen[id] = what
		return nil
	}
	for _, i := range pk.Interfaces {
		if err := add(i.GoName, "type "+i.Name, i); err != nil {
			return err
		}
		if i.HasEvents && !i.IsDisplay {
			if err := add(i.HandlerName, "handler for "+i.Name, i); err != nil {
				return err
			}
		}
		if err := add("New"+i.GoName, "constructor for "+i.Name, i); err != nil {
			return err
		}
		if !i.IsLoser {
			if err := add("Bind"+i.GoName, "bind helper for "+i.Name, i); err != nil {
				return err
			}
		}
		for _, en := range i.Enums {
			enumType := i.GoName + en.GoName
			if err := add(enumType, "enum "+i.Name+"."+en.Name, i); err != nil {
				return err
			}
			for _, e := range en.Entries {
				if err := add(enumType+e.GoName, "enum entry "+i.Name+"."+en.Name+"."+e.Name, i); err != nil {
					return err
				}
			}
		}
		for _, m := range i.Requests {
			if m.GoName == "Proxy" || m.GoName == "DispatchEvent" {
				return inputError(i.Protocol.XML, i.Name+"."+m.Name, fmt.Errorf("request method %s collides with a generated method", m.GoName))
			}
		}
	}
	return nil
}

func validateCorpus(c *corpus) error {
	for _, i := range c.interfaces {
		for n, m := range i.Requests {
			if int(m.Opcode) != n {
				return inputError(i.Protocol.XML, i.Name+"."+m.Name, fmt.Errorf("request opcode %d is not %d", m.Opcode, n))
			}
			if err := validateMessageRefs(i, m); err != nil {
				return err
			}
		}
		for n, m := range i.Events {
			if int(m.Opcode) != n {
				return inputError(i.Protocol.XML, i.Name+"."+m.Name, fmt.Errorf("event opcode %d is not %d", m.Opcode, n))
			}
			if err := validateMessageRefs(i, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMessageRefs(i *iface, m *msg) error {
	for _, a := range m.Args {
		if (a.Type == "object" || a.Type == "new_id") && a.InterfaceName != "" && a.Target == nil {
			return inputError(i.Protocol.XML, i.Name+"."+m.Name+"."+a.Name, fmt.Errorf("unresolved interface %q", a.InterfaceName))
		}
		if a.EnumName != "" && a.Enum == nil {
			return inputError(i.Protocol.XML, i.Name+"."+m.Name+"."+a.Name, fmt.Errorf("unresolved enum %q", a.EnumName))
		}
	}
	return nil
}
