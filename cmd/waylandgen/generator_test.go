package main

import (
	"bytes"
	"errors"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func waylandRoot(t *testing.T) string {
	return filepath.Join(repoRoot(t), "wayland-protocols")
}

func loadCorpusT(t *testing.T) (*corpus, []parsedSource) {
	t.Helper()
	root := waylandRoot(t)
	files, err := discoverXML(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if err := checkLayout(files); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := validateDTD(root, files); err != nil {
		t.Fatalf("DTD: %v", err)
	}
	parsed, err := parseAll(files)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c, err := buildCorpus(parsed)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return c, parsed
}

func renderT(t *testing.T, c *corpus) []renderedFile {
	t.Helper()
	files, err := renderCorpus(c)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return files
}

// minimalCoreXML is a DTD-valid core protocol used by the rejection tests.
const minimalCoreXML = `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="wayland">
  <interface name="wl_display" version="1">
    <request name="sync">
      <arg name="callback" type="new_id" interface="wl_callback"/>
    </request>
    <request name="get_registry">
      <arg name="registry" type="new_id" interface="wl_registry"/>
    </request>
  </interface>
  <interface name="wl_registry" version="1">
    <request name="bind">
      <arg name="name" type="uint"/>
      <arg name="id" type="new_id"/>
    </request>
    <event name="global">
      <arg name="name" type="uint"/>
      <arg name="interface" type="string"/>
      <arg name="version" type="uint"/>
    </event>
    <event name="global_remove">
      <arg name="name" type="uint"/>
    </event>
  </interface>
  <interface name="wl_callback" version="1">
    <event name="done">
      <arg name="callback_data" type="uint"/>
    </event>
  </interface>
</protocol>`

const validExtensionXML = `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="foo">
  <interface name="foo_manager" version="2">
    <request name="get_thing">
      <arg name="id" type="new_id" interface="foo_thing"/>
      <arg name="mode" type="uint" enum="mode"/>
    </request>
    <event name="done"/>
    <enum name="mode">
      <entry name="one" value="1"/>
      <entry name="two" value="2"/>
    </enum>
  </interface>
  <interface name="foo_thing" version="1">
    <event name="done"/>
  </interface>
</protocol>`

func writeFileT(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func copyDTD(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(waylandRoot(t), "wayland.dtd"))
	if err != nil {
		t.Fatalf("read DTD: %v", err)
	}
	writeFileT(t, filepath.Join(root, "wayland.dtd"), string(data))
}

func newValidRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyDTD(t, root)
	writeFileT(t, filepath.Join(root, "wayland.xml"), minimalCoreXML)
	return root
}

func newValidRootWithExtension(t *testing.T, xml string) string {
	t.Helper()
	root := newValidRoot(t)
	writeFileT(t, filepath.Join(root, "staging", "foo", "foo.xml"), xml)
	return root
}

func sentinelOut(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	writeFileT(t, filepath.Join(out, "keep.txt"), "keep\n")
	return out
}

func assertOutUntouched(t *testing.T, out string) {
	t.Helper()
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "keep.txt" {
		t.Fatalf("output directory changed: %v", names)
	}
	data, err := os.ReadFile(filepath.Join(out, "keep.txt"))
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("sentinel changed: %q err=%v", data, err)
	}
}

func requireGeneratorError(t *testing.T, err error, kind string) *GeneratorError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ge *GeneratorError
	if !errors.As(err, &ge) {
		t.Fatalf("error %v is not *GeneratorError", err)
	}
	if ge.Kind != kind {
		t.Fatalf("error kind = %q, want %q (%v)", ge.Kind, kind, err)
	}
	return ge
}

func TestGeneratorMetadata(t *testing.T) {
	c, parsed := loadCorpusT(t)
	if len(c.protocols) != 68 {
		t.Fatalf("protocols = %d, want 68", len(c.protocols))
	}
	if len(c.byName) != 198 {
		t.Fatalf("unique interface names = %d, want 198", len(c.byName))
	}
	if len(c.interfaces) != 211 {
		t.Fatalf("interfaces = %d, want 211", len(c.interfaces))
	}
	totalRequests, totalEvents, totalEnums := 0, 0, 0
	for _, i := range c.interfaces {
		totalRequests += len(i.Requests)
		totalEvents += len(i.Events)
		totalEnums += len(i.Enums)
	}
	if totalRequests != 598 || totalEvents != 443 || totalEnums != 195 {
		t.Fatalf("messages/enums = %d/%d/%d, want 598/443/195", totalRequests, totalEvents, totalEnums)
	}

	for _, ps := range parsed {
		proto := findProtocol(t, c, ps.Source.Rel)
		if proto.Name != ps.XML.Name {
			t.Fatalf("%s: protocol name = %q, want %q", ps.Source.Rel, proto.Name, ps.XML.Name)
		}
		if len(proto.Interfaces) != len(ps.XML.Interfaces) {
			t.Fatalf("%s: interfaces = %d, want %d", ps.Source.Rel, len(proto.Interfaces), len(ps.XML.Interfaces))
		}
		for n, xi := range ps.XML.Interfaces {
			i := proto.Interfaces[n]
			version, err := strconv.ParseUint(xi.Version, 0, 32)
			if err != nil {
				t.Fatalf("%s: version: %v", ps.Source.Rel, err)
			}
			if i.Name != xi.Name || i.Version != uint32(version) {
				t.Fatalf("%s: interface %d = %s v%d, want %s v%d", ps.Source.Rel, n, i.Name, i.Version, xi.Name, version)
			}
			wantFrozen := xi.Frozen == "true"
			if i.Frozen != wantFrozen {
				t.Fatalf("%s: %s frozen = %v, want %v", ps.Source.Rel, i.Name, i.Frozen, wantFrozen)
			}
			compareMessages(t, ps.Source.Rel, i, i.Requests, xi.Requests, true)
			compareMessages(t, ps.Source.Rel, i, i.Events, xi.Events, false)
			compareEnums(t, ps.Source.Rel, i, xi)
		}
	}

	metaVars := metaVariableNames(c)
	outputs := renderT(t, c)
	meta := findOutput(t, outputs, "interfaces_gen.go")
	for _, i := range c.interfaces {
		varName := metaVars[i]
		if strings.Count(string(meta), "var "+varName+" = &Interface{") != 1 {
			t.Fatalf("%s: metadata variable %s missing or repeated", i.Name, varName)
		}
		if !bytes.Contains(meta, []byte("\t"+varName+",\n")) {
			t.Fatalf("%s: %s missing from allInterfaces", i.Name, varName)
		}
	}
	for name, defs := range c.byName {
		winner := defs[0].Winner
		if winner.Protocol.Tier != "" && winner.Protocol.Tier != "stable" && len(defs) > 1 {
			t.Fatalf("%s: winner is not stable", name)
		}
		prefix := strconv.Quote(name) + ":"
		if !metaLineMatches(meta, prefix, metaVars[winner]+",") {
			t.Fatalf("%s: generatedInterfaces entry missing", name)
		}
		for _, d := range defs {
			if d != winner && !d.IsLoser {
				t.Fatalf("%s: duplicate %s is not marked loser", name, d.Protocol.XML)
			}
		}
	}
	// Every interface metadata variable name is unique.
	seen := map[string]bool{}
	for i, name := range metaVars {
		if seen[name] {
			t.Fatalf("metadata variable %s reused by %s", name, i.Name)
		}
		seen[name] = true
	}
}

func findProtocol(t *testing.T, c *corpus, rel string) *protocol {
	t.Helper()
	for _, p := range c.protocols {
		if p.Rel == rel {
			return p
		}
	}
	t.Fatalf("protocol %s not found", rel)
	return nil
}

func findOutput(t *testing.T, files []renderedFile, rel string) []byte {
	t.Helper()
	for _, f := range files {
		if f.Rel == rel {
			return f.Data
		}
	}
	t.Fatalf("output %s not found", rel)
	return nil
}

func compareMessages(t *testing.T, rel string, i *iface, got []*msg, want []xmlMessage, _ bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %s messages = %d, want %d", rel, i.Name, len(got), len(want))
	}
	for n, xm := range want {
		m := got[n]
		if m.Name != xm.Name {
			t.Fatalf("%s: %s message %d = %s, want %s", rel, i.Name, n, m.Name, xm.Name)
		}
		wantSince := uint32(1)
		if xm.Since != "" {
			v, err := strconv.ParseUint(xm.Since, 0, 32)
			if err != nil {
				t.Fatalf("%s: %s.%s since: %v", rel, i.Name, xm.Name, err)
			}
			wantSince = uint32(v)
		}
		if m.Since != wantSince {
			t.Fatalf("%s: %s.%s since = %d, want %d", rel, i.Name, m.Name, m.Since, wantSince)
		}
		wantDep := uint32(0)
		if xm.DeprecatedSince != "" {
			v, err := strconv.ParseUint(xm.DeprecatedSince, 0, 32)
			if err != nil {
				t.Fatalf("%s: %s.%s deprecated: %v", rel, i.Name, xm.Name, err)
			}
			wantDep = uint32(v)
		}
		if m.DeprecatedSince != wantDep {
			t.Fatalf("%s: %s.%s deprecatedSince = %d, want %d", rel, i.Name, m.Name, m.DeprecatedSince, wantDep)
		}
		if m.Destructor != (xm.Type == "destructor") {
			t.Fatalf("%s: %s.%s destructor mismatch", rel, i.Name, m.Name)
		}
		if int(m.Opcode) != n {
			t.Fatalf("%s: %s.%s opcode = %d, want %d", rel, i.Name, m.Name, m.Opcode, n)
		}
		if len(m.Args) != len(xm.Args) {
			t.Fatalf("%s: %s.%s args = %d, want %d", rel, i.Name, m.Name, len(m.Args), len(xm.Args))
		}
		for an, xa := range xm.Args {
			a := m.Args[an]
			if a.Name != xa.Name || a.Type != xa.Type {
				t.Fatalf("%s: %s.%s arg %d = %s/%s, want %s/%s", rel, i.Name, m.Name, an, a.Name, a.Type, xa.Name, xa.Type)
			}
			if a.InterfaceName != xa.Interface {
				t.Fatalf("%s: %s.%s.%s interface = %q, want %q", rel, i.Name, m.Name, a.Name, a.InterfaceName, xa.Interface)
			}
			if a.EnumName != xa.Enum {
				t.Fatalf("%s: %s.%s.%s enum = %q, want %q", rel, i.Name, m.Name, a.Name, a.EnumName, xa.Enum)
			}
			wantAllow := xa.AllowNull == "true" || xa.AllowNull == "1"
			if a.AllowNull != wantAllow {
				t.Fatalf("%s: %s.%s.%s allowNull = %v, want %v", rel, i.Name, m.Name, a.Name, a.AllowNull, wantAllow)
			}
			if (xa.Type == "object" || xa.Type == "new_id") && xa.Interface != "" {
				if a.Target == nil || a.Target.Name != xa.Interface {
					t.Fatalf("%s: %s.%s.%s unresolved target %q", rel, i.Name, m.Name, a.Name, xa.Interface)
				}
			}
			if xa.Enum != "" {
				if a.Enum == nil {
					t.Fatalf("%s: %s.%s.%s unresolved enum %q", rel, i.Name, m.Name, a.Name, xa.Enum)
				}
				wantEnum := xa.Enum
				if dot := strings.LastIndex(wantEnum, "."); dot >= 0 {
					wantEnum = wantEnum[dot+1:]
				}
				if a.Enum.Name != wantEnum {
					t.Fatalf("%s: %s.%s.%s enum = %s, want %s", rel, i.Name, m.Name, a.Name, a.Enum.Name, wantEnum)
				}
			}
		}
	}
}

func compareEnums(t *testing.T, rel string, i *iface, xi xmlInterface) {
	t.Helper()
	if len(i.Enums) != len(xi.Enums) {
		t.Fatalf("%s: %s enums = %d, want %d", rel, i.Name, len(i.Enums), len(xi.Enums))
	}
	for n, xe := range xi.Enums {
		en := i.Enums[n]
		if en.Name != xe.Name {
			t.Fatalf("%s: %s enum %d = %s, want %s", rel, i.Name, n, en.Name, xe.Name)
		}
		wantSince := uint32(1)
		if xe.Since != "" {
			v, err := strconv.ParseUint(xe.Since, 0, 32)
			if err != nil {
				t.Fatalf("%s: %s.%s since: %v", rel, i.Name, xe.Name, err)
			}
			wantSince = uint32(v)
		}
		if en.Since != wantSince {
			t.Fatalf("%s: %s.%s since = %d, want %d", rel, i.Name, en.Name, en.Since, wantSince)
		}
		if en.Bitfield != (xe.Bitfield == "true") {
			t.Fatalf("%s: %s.%s bitfield mismatch", rel, i.Name, en.Name)
		}
		if len(en.Entries) != len(xe.Entries) {
			t.Fatalf("%s: %s.%s entries = %d, want %d", rel, i.Name, en.Name, len(en.Entries), len(xe.Entries))
		}
		for m, xentry := range xe.Entries {
			e := en.Entries[m]
			wantValue, err := strconv.ParseInt(xentry.Value, 0, 64)
			if err != nil {
				t.Fatalf("%s: %s.%s.%s value: %v", rel, i.Name, en.Name, xentry.Name, err)
			}
			if e.Name != xentry.Name || e.Value != wantValue {
				t.Fatalf("%s: %s.%s entry %d = %s=%d, want %s=%d", rel, i.Name, en.Name, m, e.Name, e.Value, xentry.Name, wantValue)
			}
			wantEntrySince := uint32(1)
			if xentry.Since != "" {
				v, err := strconv.ParseUint(xentry.Since, 0, 32)
				if err != nil {
					t.Fatalf("%s: %s.%s.%s since: %v", rel, i.Name, en.Name, xentry.Name, err)
				}
				wantEntrySince = uint32(v)
			}
			if e.Since != wantEntrySince {
				t.Fatalf("%s: %s.%s.%s since = %d, want %d", rel, i.Name, en.Name, e.Name, e.Since, wantEntrySince)
			}
			wantDep := uint32(0)
			if xentry.DeprecatedSince != "" {
				v, err := strconv.ParseUint(xentry.DeprecatedSince, 0, 32)
				if err != nil {
					t.Fatalf("%s: %s.%s.%s deprecated: %v", rel, i.Name, en.Name, xentry.Name, err)
				}
				wantDep = uint32(v)
			}
			if e.DeprecatedSince != wantDep {
				t.Fatalf("%s: %s.%s.%s deprecatedSince = %d, want %d", rel, i.Name, en.Name, e.Name, e.DeprecatedSince, wantDep)
			}
		}
	}
}

func TestGeneratedLayout(t *testing.T) {
	c, _ := loadCorpusT(t)
	outputs := renderT(t, c)
	root := repoRoot(t)

	wantFiles := 2 + len(c.pkgs) - 1
	if len(outputs) != wantFiles {
		t.Fatalf("outputs = %d, want %d", len(outputs), wantFiles)
	}
	findOutput(t, outputs, "wayland_gen.go")
	findOutput(t, outputs, "interfaces_gen.go")
	for _, o := range outputs {
		if !bytes.HasPrefix(o.Data, []byte(generatedHeader)) {
			t.Fatalf("%s: missing generated header", o.Rel)
		}
		formatted, err := format.Source(o.Data)
		if err != nil {
			t.Fatalf("%s: not valid Go: %v", o.Rel, err)
		}
		if !bytes.Equal(formatted, o.Data) {
			t.Fatalf("%s: not gofmt-clean", o.Rel)
		}
		wantPkg := "wayland"
		if strings.HasPrefix(o.Rel, "protocols/") {
			parts := strings.Split(o.Rel, "/")
			wantPkg = packageName(parts[2])
			wantFile := strings.ReplaceAll(parts[2], "-", "_") + "_gen.go"
			if parts[3] != wantFile {
				t.Fatalf("%s: file name = %s, want %s", o.Rel, parts[3], wantFile)
			}
		}
		f, err := parser.ParseFile(token.NewFileSet(), o.Rel, o.Data, parser.PackageClauseOnly)
		if err != nil {
			t.Fatalf("%s: parse package: %v", o.Rel, err)
		}
		if f.Name.Name != wantPkg {
			t.Fatalf("%s: package = %s, want %s", o.Rel, f.Name.Name, wantPkg)
		}
		onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(o.Rel)))
		if err != nil {
			t.Fatalf("%s: not generated on disk (run make generate): %v", o.Rel, err)
		}
		if !bytes.Equal(onDisk, o.Data) {
			t.Fatalf("%s: committed output is stale (run make generate)", o.Rel)
		}
	}
	// No extra or handwritten Go files under protocols/.
	want := map[string]bool{}
	for _, o := range outputs {
		want[o.Rel] = true
	}
	var found []string
	err := filepath.WalkDir(filepath.Join(root, "protocols"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".go") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk protocols: %v", err)
	}
	if len(found) != len(outputs)-2 {
		t.Fatalf("protocol .go files = %d, want %d", len(found), len(outputs)-2)
	}
	for _, rel := range found {
		if !want[rel] {
			t.Fatalf("unexpected generated file %s", rel)
		}
		if strings.HasSuffix(rel, "_test.go") {
			t.Fatalf("test file under protocols/: %s", rel)
		}
	}
}

var hostPathRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])(/home/|/Users/|[A-Za-z]:\\)`)
var timestampRE = regexp.MustCompile(`\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b`)

func TestGeneratedNoHostPaths(t *testing.T) {
	c, _ := loadCorpusT(t)
	outputs := renderT(t, c)
	root := repoRoot(t)
	for _, o := range outputs {
		s := string(o.Data)
		if !strings.HasPrefix(s, generatedHeader) {
			t.Fatalf("%s: missing generated header", o.Rel)
		}
		if strings.Contains(s, root) {
			t.Fatalf("%s: contains host checkout path", o.Rel)
		}
		if strings.Contains(s, "wayland-protocols/") {
			t.Fatalf("%s: contains input path", o.Rel)
		}
		if hostPathRE.MatchString(s) {
			t.Fatalf("%s: contains host absolute path", o.Rel)
		}
		if timestampRE.MatchString(s) {
			t.Fatalf("%s: contains a timestamp", o.Rel)
		}
	}
}

func TestGeneratedDeterministicRender(t *testing.T) {
	root := waylandRoot(t)
	files, err := discoverXML(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	parsed, err := parseAll(files)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c1, err := buildCorpus(parsed)
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	c2, err := buildCorpus(parsed)
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	out1 := renderT(t, c1)
	out2 := renderT(t, c2)
	if len(out1) != len(out2) {
		t.Fatalf("render counts differ: %d vs %d", len(out1), len(out2))
	}
	for n := range out1 {
		if out1[n].Rel != out2[n].Rel {
			t.Fatalf("render order differs at %d: %s vs %s", n, out1[n].Rel, out2[n].Rel)
		}
		if !bytes.Equal(out1[n].Data, out2[n].Data) {
			t.Fatalf("%s: render is not deterministic", out1[n].Rel)
		}
	}
}

func TestGeneratorRejectsInvalidInput(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		root := t.TempDir()
		copyDTD(t, root)
		out := sentinelOut(t)
		err := generate(options{Root: root, Out: out})
		_ = requireGeneratorError(t, err, "input")
		assertOutUntouched(t, out)
	})
	t.Run("missing DTD", func(t *testing.T) {
		root := t.TempDir()
		writeFileT(t, filepath.Join(root, "wayland.xml"), minimalCoreXML)
		out := sentinelOut(t)
		err := generate(options{Root: root, Out: out})
		ge := requireGeneratorError(t, err, "input")
		if !strings.Contains(ge.Error(), "wayland.dtd") {
			t.Fatalf("error does not name DTD: %v", err)
		}
		assertOutUntouched(t, out)
	})
	t.Run("missing xmllint", func(t *testing.T) {
		root := newValidRoot(t)
		out := sentinelOut(t)
		t.Setenv("PATH", "")
		err := generate(options{Root: root, Out: out})
		ge := requireGeneratorError(t, err, "input")
		if !strings.Contains(ge.Error(), "xmllint") {
			t.Fatalf("error does not mention xmllint: %v", err)
		}
		assertOutUntouched(t, out)
	})
	t.Run("malformed XML", func(t *testing.T) {
		root := t.TempDir()
		copyDTD(t, root)
		writeFileT(t, filepath.Join(root, "wayland.xml"), "<protocol")
		out := sentinelOut(t)
		err := generate(options{Root: root, Out: out})
		ge := requireGeneratorError(t, err, "input")
		if !strings.Contains(ge.Path, "wayland.xml") {
			t.Fatalf("error does not name file: %v", err)
		}
		assertOutUntouched(t, out)
	})
	t.Run("DTD violation", func(t *testing.T) {
		root := t.TempDir()
		copyDTD(t, root)
		writeFileT(t, filepath.Join(root, "wayland.xml"),
			`<protocol name="wayland"><interface name="wl_display" version="1"><bogus/></interface></protocol>`)
		out := sentinelOut(t)
		err := generate(options{Root: root, Out: out})
		ge := requireGeneratorError(t, err, "input")
		if !strings.Contains(ge.Error(), "wayland.xml") {
			t.Fatalf("error does not name file: %v", err)
		}
		assertOutUntouched(t, out)
	})
}

func TestGeneratorRejectsInvalidModel(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want string
	}{
		{"since above version", strings.Replace(validExtensionXML, `name="get_thing"`, `name="get_thing" since="3"`, 1), "since"},
		{"deprecated below since", strings.Replace(validExtensionXML, `name="get_thing"`, `name="get_thing" since="2" deprecated-since="1"`, 1), "deprecated-since"},
		{"unknown object interface", strings.Replace(validExtensionXML, `<arg name="mode" type="uint" enum="mode"/>`, `<arg name="thing" type="object" interface="no_such"/>`, 1), "unknown interface"},
		{"unknown new_id interface", strings.Replace(validExtensionXML, `interface="foo_thing"`, `interface="no_such"`, 1), "unknown interface"},
		{"unknown enum", strings.Replace(validExtensionXML, `enum="mode"`, `enum="nope"`, 1), "unknown enum"},
		{"allow-null on int", strings.Replace(validExtensionXML, `<arg name="mode" type="uint" enum="mode"/>`, `<arg name="mode" type="uint" allow-null="true"/>`, 1), "allow-null"},
		{"unknown arg type", strings.Replace(validExtensionXML, `type="uint" enum="mode"`, `type="float"`, 1), "unsupported arg type"},
		{"dynamic new_id outside registry", strings.Replace(validExtensionXML, `<arg name="id" type="new_id" interface="foo_thing"/>`, `<arg name="id" type="new_id"/>`, 1), "dynamic new_id"},
		{"enum on string", strings.Replace(validExtensionXML, `<arg name="mode" type="uint" enum="mode"/>`, `<arg name="mode" type="string" enum="mode"/>`, 1), "enum is only valid"},
		{"interface on int", strings.Replace(validExtensionXML, `<arg name="mode" type="uint" enum="mode"/>`, `<arg name="mode" type="uint" interface="foo_thing"/>`, 1), "interface is only valid"},
		{"duplicate request", strings.Replace(validExtensionXML, `<request name="get_thing">`, `<request name="get_thing"/><request name="get_thing">`, 1), "duplicate request"},
		{"duplicate interface", strings.Replace(validExtensionXML, `</protocol>`, `<interface name="foo_manager" version="1"><event name="done"/></interface></protocol>`, 1), "duplicate interface"},
		{"duplicate enum", strings.Replace(validExtensionXML, `<enum name="mode">`, `<enum name="mode"><entry name="zero" value="0"/></enum><enum name="mode">`, 1), "duplicate enum"},
		{"duplicate enum value", strings.Replace(validExtensionXML, `<entry name="two" value="2"/>`, `<entry name="two" value="1"/>`, 1), "duplicate enum value"},
		{"type name collision", `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="foo">
  <interface name="foo_a" version="1"><event name="done"/></interface>
  <interface name="foo__a" version="1"><event name="done"/></interface>
</protocol>`, "collides"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newValidRootWithExtension(t, tc.xml)
			out := sentinelOut(t)
			err := generate(options{Root: root, Out: out})
			ge := requireGeneratorError(t, err, "input")
			if !strings.Contains(strings.ToLower(ge.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error %q does not mention %q", ge.Error(), tc.want)
			}
			assertOutUntouched(t, out)
		})
	}
	t.Run("ambiguous duplicate", func(t *testing.T) {
		root := newValidRoot(t)
		dup := `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="dup">
  <interface name="dup_iface" version="1"><event name="done"/></interface>
</protocol>`
		writeFileT(t, filepath.Join(root, "staging", "dup-a", "dup-a.xml"), dup)
		writeFileT(t, filepath.Join(root, "unstable", "dup-b", "dup-b.xml"), dup)
		out := sentinelOut(t)
		err := generate(options{Root: root, Out: out})
		ge := requireGeneratorError(t, err, "input")
		if !strings.Contains(ge.Error(), "ambiguous") {
			t.Fatalf("error does not mention ambiguity: %v", err)
		}
		assertOutUntouched(t, out)
	})
	t.Run("non-contiguous opcode", func(t *testing.T) {
		c, _ := loadCorpusT(t)
		c.protocols[0].Interfaces[0].Requests[0].Opcode = 5
		if err := validateCorpus(c); err == nil {
			t.Fatal("expected opcode validation error")
		}
	})
}

func TestGeneratorWriteError(t *testing.T) {
	t.Run("output is a file", func(t *testing.T) {
		root := newValidRoot(t)
		out := filepath.Join(t.TempDir(), "out-file")
		writeFileT(t, out, "not a directory\n")
		err := generate(options{Root: root, Out: out})
		if !IsWriteError(err) {
			t.Fatalf("error = %v, want write error", err)
		}
		data, readErr := os.ReadFile(out)
		if readErr != nil || string(data) != "not a directory\n" {
			t.Fatalf("sentinel changed: %q err=%v", data, readErr)
		}
	})
	t.Run("read-only output", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root; permission bits are not enforced")
		}
		root := newValidRoot(t)
		out := t.TempDir()
		if err := os.Chmod(out, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(out, 0o755) })
		err := generate(options{Root: root, Out: out})
		if !IsWriteError(err) {
			t.Fatalf("error = %v, want write error", err)
		}
	})
}

func TestRegistryBindWireArgs(t *testing.T) {
	c, _ := loadCorpusT(t)
	registry := c.root.byName["wl_registry"]
	if registry == nil {
		t.Fatal("wl_registry missing")
	}
	var bind *msg
	for _, m := range registry.Requests {
		if m.Name == "bind" {
			bind = m
		}
	}
	if bind == nil {
		t.Fatal("wl_registry.bind missing")
	}
	if len(bind.Args) != 2 || bind.Args[0].Name != "name" || bind.Args[1].Name != "id" {
		t.Fatalf("wl_registry.bind args = %+v, want name/id only", bind.Args)
	}
	if !bind.Args[1].Dynamic {
		t.Fatal("wl_registry.bind new_id is not dynamic")
	}
}

func metaLineMatches(data []byte, prefix, suffix string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, suffix) {
			return true
		}
	}
	return false
}

func TestGeneratorImportAliasCollisions(t *testing.T) {
	root := newValidRoot(t)
	writeFileT(t, filepath.Join(root, "staging", "foo-bar", "foo-bar.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="foo_bar_a">
  <interface name="alpha_one" version="1"><event name="done"/></interface>
</protocol>`)
	writeFileT(t, filepath.Join(root, "unstable", "foo_bar", "foo_bar.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="foo_bar_b">
  <interface name="beta_two" version="1"><event name="done"/></interface>
</protocol>`)
	writeFileT(t, filepath.Join(root, "staging", "ref", "ref.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<protocol name="ref">
  <interface name="ref_manager" version="1">
    <request name="use">
      <arg name="a" type="object" interface="alpha_one"/>
      <arg name="b" type="object" interface="beta_two"/>
    </request>
  </interface>
</protocol>`)
	out := t.TempDir()
	if err := generate(options{Root: root, Out: out}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "protocols", "staging", "ref", "ref_gen.go"))
	if err != nil {
		t.Fatalf("read ref output: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"github.com/xogas/wayland/protocols/staging/foo-bar"`) {
		t.Fatalf("missing first same-name package import:\n%s", s)
	}
	if !strings.Contains(s, `unstablefoobar "github.com/xogas/wayland/protocols/unstable/foo_bar"`) {
		t.Fatalf("missing disambiguated import alias:\n%s", s)
	}
	if !strings.Contains(s, "a *foobar.AlphaOne") || !strings.Contains(s, "b *unstablefoobar.BetaTwo") {
		t.Fatalf("cross-package parameter types are not disambiguated:\n%s", s)
	}
}
