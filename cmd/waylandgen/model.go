package main

import (
	"fmt"
	"go/token"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const modulePath = "github.com/xogas/wayland"

var nameRE = regexp.MustCompile(`^[a-z0-9_]+$`)

type corpus struct {
	root       *pkg
	pkgs       []*pkg
	protocols  []*protocol
	interfaces []*iface
	byName     map[string][]*iface
}

type pkg struct {
	Tier       string
	Dir        string
	ImportPath string
	Name       string
	OutFile    string
	Protocols  []*protocol
	Interfaces []*iface
	LocalMeta  []*iface
	byName     map[string]*iface
}

type protocol struct {
	Name       string
	XML        string
	Rel        string
	Tier       string
	Dir        string
	Pkg        *pkg
	Interfaces []*iface
}

type iface struct {
	Name        string
	Version     uint32
	Frozen      bool
	Requests    []*msg
	Events      []*msg
	Enums       []*enum
	ShortGoName string
	GoName      string
	HandlerName string
	HasEvents   bool
	IsDisplay   bool
	IsLoser     bool
	Winner      *iface
	Protocol    *protocol
	Pkg         *pkg
}

type msg struct {
	Name            string
	Since           uint32
	DeprecatedSince uint32
	Destructor      bool
	Args            []*arg
	GoName          string
	Opcode          uint16
	IsRequest       bool
}

type arg struct {
	Name            string
	Type            string
	InterfaceName   string
	EnumName        string
	AllowNull       bool
	Since           uint32
	DeprecatedSince uint32
	Dynamic         bool
	Target          *iface
	EnumTarget      *iface
	Enum            *enum
}

type enum struct {
	Name     string
	Since    uint32
	Bitfield bool
	Entries  []*entry
	GoName   string
}

type entry struct {
	Name            string
	Value           int64
	Since           uint32
	DeprecatedSince uint32
	GoName          string
}

// buildCorpus turns parsed XML into the naming, reference and metadata model.
func buildCorpus(parsed []parsedSource) (*corpus, error) {
	c := &corpus{byName: make(map[string][]*iface)}
	pkgsByKey := make(map[string]*pkg)
	order := make([]*pkg, 0)
	protocols := make([]*protocol, 0, len(parsed))

	for _, ps := range parsed {
		pk, err := packageFor(ps.Source, pkgsByKey)
		if err != nil {
			return nil, err
		}
		if !containsPkg(order, pk) {
			order = append(order, pk)
		}
		proto, err := buildProtocol(ps, pk)
		if err != nil {
			return nil, err
		}
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
	if c.root == nil {
		return nil, inputError("", "layout", fmt.Errorf("missing wayland.xml at the generator root"))
	}
	c.protocols = protocols

	for _, pk := range c.pkgs {
		if err := assignPackageNames(pk); err != nil {
			return nil, err
		}
		pk.byName = make(map[string]*iface, len(pk.Interfaces))
		for _, i := range pk.Interfaces {
			if prev, ok := pk.byName[i.Name]; ok {
				return nil, inputError(i.Protocol.XML, i.Name, fmt.Errorf("duplicate interface %s in package %s (also in %s)", i.Name, pk.Name, prev.Protocol.XML))
			}
			pk.byName[i.Name] = i
		}
	}
	for _, proto := range c.protocols {
		c.interfaces = append(c.interfaces, proto.Interfaces...)
	}
	for _, i := range c.interfaces {
		c.byName[i.Name] = append(c.byName[i.Name], i)
	}
	if err := resolveDuplicates(c); err != nil {
		return nil, err
	}
	for _, i := range c.interfaces {
		if i.IsLoser {
			i.Pkg.LocalMeta = append(i.Pkg.LocalMeta, i)
		}
	}
	for _, pk := range c.pkgs {
		if err := resolvePackage(c, pk); err != nil {
			return nil, err
		}
	}
	for _, pk := range c.pkgs {
		if err := validateIdentifiers(pk); err != nil {
			return nil, err
		}
	}
	if err := validateCorpus(c); err != nil {
		return nil, err
	}
	return c, nil
}

func containsPkg(pkgs []*pkg, want *pkg) bool {
	for _, p := range pkgs {
		if p == want {
			return true
		}
	}
	return false
}

func packageFor(src sourceFile, seen map[string]*pkg) (*pkg, error) {
	if src.Rel == "wayland.xml" {
		if p := seen[""]; p != nil {
			return p, nil
		}
		p := &pkg{
			Name:    "wayland",
			OutFile: "protocol_gen.go",
		}
		seen[""] = p
		return p, nil
	}
	parts := strings.Split(src.Rel, "/")
	if len(parts) != 3 {
		return nil, inputError(src.Path, "layout", fmt.Errorf("want <tier>/<dir>/<name>.xml, got %s", src.Rel))
	}
	tier, dir := parts[0], parts[1]
	key := tier + "/" + dir
	if p := seen[key]; p != nil {
		return p, nil
	}
	name := packageName(dir)
	if name == "" || !token.IsIdentifier(name) || token.IsKeyword(name) {
		return nil, inputError(src.Path, "package", fmt.Errorf("invalid Go package name %q for directory %q", name, dir))
	}
	file := strings.ReplaceAll(dir, "-", "_") + "_gen.go"
	p := &pkg{
		Tier:       tier,
		Dir:        dir,
		ImportPath: modulePath + "/protocols/" + tier + "/" + dir,
		Name:       name,
		OutFile:    "protocols/" + tier + "/" + dir + "/" + file,
	}
	seen[key] = p
	return p, nil
}

func buildProtocol(ps parsedSource, pk *pkg) (*protocol, error) {
	xp := ps.XML
	if xp.Name == "" || !nameRE.MatchString(xp.Name) {
		return nil, inputError(ps.Source.Path, "protocol.name", fmt.Errorf("invalid protocol name %q", xp.Name))
	}
	if len(xp.Interfaces) == 0 {
		return nil, inputError(ps.Source.Path, "interfaces", fmt.Errorf("protocol %s has no interfaces", xp.Name))
	}
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
	seenIface := make(map[string]bool, len(xp.Interfaces))
	for _, xi := range xp.Interfaces {
		if xi.Name == "" || !nameRE.MatchString(xi.Name) {
			return nil, inputError(ps.Source.Path, "interface.name", fmt.Errorf("invalid interface name %q", xi.Name))
		}
		if seenIface[xi.Name] {
			return nil, inputError(ps.Source.Path, xi.Name, fmt.Errorf("duplicate interface %s", xi.Name))
		}
		seenIface[xi.Name] = true
		names = append(names, xi.Name)
	}
	prefix := commonPrefix(names)
	if pk.ImportPath == "" {
		prefix = "wl_"
	}
	for _, xi := range xp.Interfaces {
		i, err := buildInterface(xi, prefix, ps.Source.Path, proto)
		if err != nil {
			return nil, err
		}
		proto.Interfaces = append(proto.Interfaces, i)
		pk.Interfaces = append(pk.Interfaces, i)
	}
	return proto, nil
}

func buildInterface(x xmlInterface, prefix, path string, proto *protocol) (*iface, error) {
	version, err := parseUint32(x.Version, 0, path, x.Name+".version")
	if err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, inputError(path, x.Name+".version", fmt.Errorf("version must be at least 1, got %d", version))
	}
	frozen, _, err := parseBool(x.Frozen, path, x.Name+".frozen")
	if err != nil {
		return nil, err
	}
	short := strings.TrimPrefix(x.Name, prefix)
	if short == "" && prefix != "" {
		return nil, inputError(path, x.Name, fmt.Errorf("cannot strip protocol prefix %q", prefix))
	}
	goName := camel(short)
	if !token.IsIdentifier(goName) || !token.IsExported(goName) {
		return nil, inputError(path, x.Name, fmt.Errorf("invalid Go type name %q from interface %q", goName, x.Name))
	}
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
	reqNames := make(map[string]bool, len(x.Requests))
	reqGo := make(map[string]bool, len(x.Requests))
	for _, r := range x.Requests {
		m, err := buildMessage(r, true, version, path, x.Name)
		if err != nil {
			return nil, err
		}
		if reqNames[m.Name] {
			return nil, inputError(path, x.Name+"."+m.Name, fmt.Errorf("duplicate request"))
		}
		if reqGo[m.GoName] {
			return nil, inputError(path, x.Name+"."+m.Name, fmt.Errorf("duplicate request Go name %s", m.GoName))
		}
		reqNames[m.Name] = true
		reqGo[m.GoName] = true
		m.Opcode = uint16(len(i.Requests))
		i.Requests = append(i.Requests, m)
	}
	evNames := make(map[string]bool, len(x.Events))
	evGo := make(map[string]bool, len(x.Events))
	for _, e := range x.Events {
		m, err := buildMessage(e, false, version, path, x.Name)
		if err != nil {
			return nil, err
		}
		if evNames[m.Name] {
			return nil, inputError(path, x.Name+"."+m.Name, fmt.Errorf("duplicate event"))
		}
		if evGo[m.GoName] {
			return nil, inputError(path, x.Name+"."+m.Name, fmt.Errorf("duplicate event Go name %s", m.GoName))
		}
		evNames[m.Name] = true
		evGo[m.GoName] = true
		m.Opcode = uint16(len(i.Events))
		i.Events = append(i.Events, m)
	}
	i.HasEvents = len(i.Events) > 0
	enumNames := make(map[string]bool, len(x.Enums))
	for _, e := range x.Enums {
		en, err := buildEnum(e, version, path, x.Name)
		if err != nil {
			return nil, err
		}
		if enumNames[en.Name] {
			return nil, inputError(path, x.Name+"."+en.Name, fmt.Errorf("duplicate enum"))
		}
		enumNames[en.Name] = true
		i.Enums = append(i.Enums, en)
	}
	return i, nil
}

func buildMessage(x xmlMessage, request bool, version uint32, path, ifaceName string) (*msg, error) {
	kind := "event"
	if request {
		kind = "request"
	}
	if x.Name == "" || !nameRE.MatchString(x.Name) {
		return nil, inputError(path, ifaceName+"."+x.Name, fmt.Errorf("invalid %s name %q", kind, x.Name))
	}
	since, err := parseSince(x.Since, path, ifaceName+"."+x.Name+".since")
	if err != nil {
		return nil, err
	}
	if since > version {
		return nil, inputError(path, ifaceName+"."+x.Name+".since", fmt.Errorf("since %d exceeds interface version %d", since, version))
	}
	deprecated, err := parseDeprecated(x.DeprecatedSince, since, path, ifaceName+"."+x.Name+".deprecated-since")
	if err != nil {
		return nil, err
	}
	switch x.Type {
	case "":
	case "destructor":
	default:
		return nil, inputError(path, ifaceName+"."+x.Name+".type", fmt.Errorf("unsupported message type %q", x.Type))
	}
	m := &msg{
		Name:            x.Name,
		Since:           since,
		DeprecatedSince: deprecated,
		Destructor:      x.Type == "destructor",
		GoName:          camel(x.Name),
		IsRequest:       request,
	}
	if !token.IsIdentifier(m.GoName) {
		return nil, inputError(path, ifaceName+"."+x.Name, fmt.Errorf("invalid Go method name %q", m.GoName))
	}
	argNames := make(map[string]bool, len(x.Args))
	newIDCount := 0
	for _, a := range x.Args {
		aa, err := buildArg(a, path, ifaceName, x.Name)
		if err != nil {
			return nil, err
		}
		if argNames[aa.Name] {
			return nil, inputError(path, ifaceName+"."+x.Name+"."+aa.Name, fmt.Errorf("duplicate arg"))
		}
		argNames[aa.Name] = true
		if aa.Type == "new_id" {
			newIDCount++
		}
		m.Args = append(m.Args, aa)
	}
	if newIDCount > 1 {
		return nil, inputError(path, ifaceName+"."+x.Name, fmt.Errorf("messages with more than one new_id are not supported"))
	}
	return m, nil
}

func buildArg(x xmlArg, path, ifaceName, msgName string) (*arg, error) {
	field := ifaceName + "." + msgName + "." + x.Name
	if x.Name == "" || !nameRE.MatchString(x.Name) {
		return nil, inputError(path, ifaceName+"."+msgName, fmt.Errorf("invalid arg name %q", x.Name))
	}
	switch x.Type {
	case "int", "uint", "fixed", "string", "array", "fd", "new_id", "object":
	default:
		return nil, inputError(path, field, fmt.Errorf("unsupported arg type %q", x.Type))
	}
	allow, _, err := parseBool(x.AllowNull, path, field+".allow-null")
	if err != nil {
		return nil, err
	}
	if allow && x.Type != "string" && x.Type != "array" && x.Type != "new_id" && x.Type != "object" {
		return nil, inputError(path, field, fmt.Errorf("allow-null is not valid on %s", x.Type))
	}
	if x.Interface != "" && x.Type != "object" && x.Type != "new_id" {
		return nil, inputError(path, field+".interface", fmt.Errorf("interface is only valid on object and new_id args"))
	}
	if x.Enum != "" && x.Type != "int" && x.Type != "uint" {
		return nil, inputError(path, field+".enum", fmt.Errorf("enum is only valid on int and uint args"))
	}
	return &arg{
		Name:          x.Name,
		Type:          x.Type,
		InterfaceName: x.Interface,
		EnumName:      x.Enum,
		AllowNull:     allow,
	}, nil
}

func buildEnum(x xmlEnum, version uint32, path, ifaceName string) (*enum, error) {
	field := ifaceName + "." + x.Name
	if x.Name == "" || !nameRE.MatchString(x.Name) {
		return nil, inputError(path, ifaceName, fmt.Errorf("invalid enum name %q", x.Name))
	}
	since, err := parseSince(x.Since, path, field+".since")
	if err != nil {
		return nil, err
	}
	if since > version {
		return nil, inputError(path, field+".since", fmt.Errorf("since %d exceeds interface version %d", since, version))
	}
	bitfield, _, err := parseBool(x.Bitfield, path, field+".bitfield")
	if err != nil {
		return nil, err
	}
	en := &enum{Name: x.Name, Since: since, Bitfield: bitfield, GoName: camel(x.Name)}
	if en.GoName == "" {
		return nil, inputError(path, field, fmt.Errorf("empty Go enum name"))
	}
	names := make(map[string]bool, len(x.Entries))
	values := make(map[int64]bool, len(x.Entries))
	for _, xe := range x.Entries {
		if xe.Name == "" || !nameRE.MatchString(xe.Name) {
			return nil, inputError(path, field, fmt.Errorf("invalid entry name %q", xe.Name))
		}
		if names[xe.Name] {
			return nil, inputError(path, field+"."+xe.Name, fmt.Errorf("duplicate enum entry"))
		}
		names[xe.Name] = true
		v, err := strconv.ParseInt(xe.Value, 0, 64)
		if err != nil {
			return nil, inputError(path, field+"."+xe.Name+".value", fmt.Errorf("parse %q: %w", xe.Value, err))
		}
		if v < math.MinInt32 || v > math.MaxUint32 {
			return nil, inputError(path, field+"."+xe.Name+".value", fmt.Errorf("value %d does not fit in 32 bits", v))
		}
		if values[v] {
			return nil, inputError(path, field+"."+xe.Name+".value", fmt.Errorf("duplicate enum value %d", v))
		}
		values[v] = true
		es, err := parseSince(xe.Since, path, field+"."+xe.Name+".since")
		if err != nil {
			return nil, err
		}
		if es > version {
			return nil, inputError(path, field+"."+xe.Name+".since", fmt.Errorf("since %d exceeds interface version %d", es, version))
		}
		ed, err := parseDeprecated(xe.DeprecatedSince, es, path, field+"."+xe.Name+".deprecated-since")
		if err != nil {
			return nil, err
		}
		goName := camel(xe.Name)
		if goName == "" {
			return nil, inputError(path, field+"."+xe.Name, fmt.Errorf("empty Go entry name"))
		}
		en.Entries = append(en.Entries, &entry{Name: xe.Name, Value: v, Since: es, DeprecatedSince: ed, GoName: goName})
	}
	// Underlying type: int32 by default, uint32 for bitfields or high values.
	unsigned := en.Bitfield
	for _, e := range en.Entries {
		if e.Value > math.MaxInt32 {
			unsigned = true
		}
	}
	if unsigned {
		for _, e := range en.Entries {
			if e.Value < 0 || e.Value > math.MaxUint32 {
				return nil, inputError(path, field+"."+e.Name+".value", fmt.Errorf("value %d does not fit in uint32", e.Value))
			}
		}
	} else {
		for _, e := range en.Entries {
			if e.Value < math.MinInt32 || e.Value > math.MaxInt32 {
				return nil, inputError(path, field+"."+e.Name+".value", fmt.Errorf("value %d does not fit in int32", e.Value))
			}
		}
	}
	return en, nil
}

func parseUint32(s string, def uint32, path, field string) (uint32, error) {
	if s == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return 0, inputError(path, field, fmt.Errorf("parse %q: %w", s, err))
	}
	return uint32(v), nil
}

func parseSince(s, path, field string) (uint32, error) {
	v, err := parseUint32(s, 1, path, field)
	if err != nil {
		return 0, err
	}
	if v < 1 {
		return 0, inputError(path, field, fmt.Errorf("since must be at least 1, got %d", v))
	}
	return v, nil
}

func parseDeprecated(s string, since uint32, path, field string) (uint32, error) {
	if s == "" {
		return 0, nil
	}
	v, err := parseUint32(s, 0, path, field)
	if err != nil {
		return 0, err
	}
	if v < since {
		return 0, inputError(path, field, fmt.Errorf("deprecated-since %d is below since %d", v, since))
	}
	return v, nil
}

func parseBool(s, path, field string) (bool, bool, error) {
	switch s {
	case "":
		return false, false, nil
	case "true", "1":
		return true, true, nil
	case "false", "0":
		return false, true, nil
	default:
		return false, false, inputError(path, field, fmt.Errorf("invalid boolean %q", s))
	}
}

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

func resolvePackage(c *corpus, pk *pkg) error {
	for _, i := range pk.Interfaces {
		for _, m := range i.Requests {
			for _, a := range m.Args {
				if err := resolveArg(c, i, m, a); err != nil {
					return err
				}
			}
		}
		for _, m := range i.Events {
			for _, a := range m.Args {
				if err := resolveArg(c, i, m, a); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func resolveArg(c *corpus, i *iface, m *msg, a *arg) error {
	path := i.Protocol.XML
	field := i.Name + "." + m.Name + "." + a.Name
	if a.InterfaceName != "" {
		target, err := resolveInterface(c, i, a.InterfaceName)
		if err != nil {
			return inputError(path, field+".interface", err)
		}
		a.Target = target
	} else if a.Type == "object" {
		if m.IsRequest {
			return inputError(path, field+".interface", fmt.Errorf("object request args require an interface"))
		}
	} else if a.Type == "new_id" {
		if i.Name != "wl_registry" || m.Name != "bind" || i.Pkg.ImportPath != "" {
			return inputError(path, field+".interface", fmt.Errorf("dynamic new_id is only valid on wl_registry.bind"))
		}
		a.Dynamic = true
	}
	if a.EnumName != "" {
		enumIface := i
		enumName := a.EnumName
		if dot := strings.LastIndex(enumName, "."); dot >= 0 {
			qualifier := enumName[:dot]
			enumName = enumName[dot+1:]
			target, err := resolveInterface(c, i, qualifier)
			if err != nil {
				return inputError(path, field+".enum", err)
			}
			enumIface = target
		}
		en := findEnum(enumIface, enumName)
		if en == nil {
			return inputError(path, field+".enum", fmt.Errorf("unknown enum %s.%s", enumIface.Name, enumName))
		}
		a.EnumTarget = enumIface
		a.Enum = en
	}
	return nil
}

func resolveInterface(c *corpus, src *iface, name string) (*iface, error) {
	if local, ok := src.Pkg.byName[name]; ok {
		return local, nil
	}
	defs := c.byName[name]
	if len(defs) == 0 {
		return nil, fmt.Errorf("unknown interface %q", name)
	}
	return defs[0].Winner, nil
}

func findEnum(i *iface, name string) *enum {
	for _, en := range i.Enums {
		if en.Name == name {
			return en
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

func packageName(dir string) string {
	s := strings.NewReplacer("-", "", "_", "").Replace(dir)
	return strings.ToLower(s)
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
		if p == "id" {
			b.WriteString("ID")
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
