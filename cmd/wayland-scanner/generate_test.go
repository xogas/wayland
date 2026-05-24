package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestNamingConversions(t *testing.T) {
	tests := []struct {
		input    string
		pascal   string
		camel    string
		typeName string
	}{
		{"display", "Display", "display", "Display"},
		{"registry", "Registry", "registry", "Registry"},
		{"shm_pool", "ShmPool", "shmPool", "ShmPool"},
		{"data_device_manager", "DataDeviceManager", "dataDeviceManager", "DataDeviceManager"},
		{"object_id", "ObjectID", "objectID", "ObjectID"},
		{"new_id", "NewID", "newID", "NewID"},
		{"delete_id", "DeleteID", "deleteID", "DeleteID"},
		{"interface", "Interface", "interface_", "Interface"},
		{"class_", "Class", "class", "Class"},
		{"90", "90", "90", "90"},
		{"flipped_90", "Flipped90", "flipped90", "Flipped90"},
		{"global_remove", "GlobalRemove", "globalRemove", "GlobalRemove"},
		{"dnd_action", "DndAction", "dndAction", "DndAction"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pascal(tt.input)
			if got != tt.pascal {
				t.Errorf("pascal(%q) = %q, want %q", tt.input, got, tt.pascal)
			}

			gotC := camel(tt.input)
			if gotC != tt.camel {
				t.Errorf("camel(%q) = %q, want %q", tt.input, gotC, tt.camel)
			}
		})
	}

	// Entry name without prefix -- pascal handles digits properly
	if got := pascal("90"); got != "90" {
		t.Errorf("pascal(\"90\") = %q, want %q", got, "90")
	}
	if got := pascal("normal"); got != "Normal" {
		t.Errorf("pascal(\"normal\") = %q, want %q", got, "Normal")
	}

	// typeName with prefix
	if got := typeName("wl_display", "wl_", ""); got != "Display" {
		t.Errorf("typeName(wl_display, wl_) = %q, want Display", got)
	}
	if got := typeName("xdg_wm_base", "xdg_", ""); got != "WmBase" {
		t.Errorf("typeName(xdg_wm_base, xdg_) = %q, want WmBase", got)
	}
}

func TestSnakeCase(t *testing.T) {
	tests := []struct{ input, want string }{
		{"Display", "display"},
		{"DataDeviceManager", "data_device_manager"},
		{"ShmPool", "shm_pool"},
		{"Registry", "registry"},
	}
	for _, tt := range tests {
		if got := snakeCase(tt.input); got != tt.want {
			t.Errorf("snakeCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateWaylandXML(t *testing.T) {
	proto, err := Parse("../../wayland-protocols/wayland.xml")
	if err != nil {
		t.Fatal(err)
	}

	tmpDir, err := os.MkdirTemp("", "wayland-scanner-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir) //nolint: errcheck

	cfg := GenerateConfig{
		OutDir:     tmpDir,
		Package:    "testpkg",
		TrimPrefix: "wl_",
	}

	if err := Generate(proto, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Parse generated files to verify they compile
	files, err := filepath.Glob(filepath.Join(tmpDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 4 {
		t.Fatalf("expected at least 4 generated files, got %d", len(files))
	}

	fset := token.NewFileSet()
	for _, f := range files {
		_, err := parser.ParseFile(fset, f, nil, parser.AllErrors)
		if err != nil {
			t.Errorf("parse %s: %v", filepath.Base(f), err)
		}
	}
}

func TestCrossValidateGeneratedVsXML(t *testing.T) {
	proto, err := Parse("../../wayland-protocols/wayland.xml")
	if err != nil {
		t.Fatal(err)
	}

	cfg := GenerateConfig{
		Package:    "wayland",
		TrimPrefix: "wl_",
	}

	ifaceSet := make(map[string]bool, len(proto.Interfaces))
	for i := range proto.Interfaces {
		ifaceSet[proto.Interfaces[i].Name] = true
	}

	for _, iface := range proto.Interfaces {
		t.Run(iface.Name, func(t *testing.T) {
			d, err := buildData(&iface, typeName(iface.Name, "wl_", ""),
				cfg, ifaceSet)
			if err != nil {
				t.Fatalf("buildData: %v", err)
			}

			// 1. Opcode ordering
			for idx, req := range d.Requests {
				if req.Opcode != idx {
					t.Errorf("request %s opcode=%d, want %d (index in XML)", req.Name, req.Opcode, idx)
				}
			}
			for idx, ev := range d.Events {
				if ev.Opcode != idx {
					t.Errorf("event %s opcode=%d, want %d (index in XML)", ev.Name, ev.Opcode, idx)
				}
			}

			// 2. Since values
			for _, req := range iface.Requests {
				reqName := pascal(req.Name)
				tname := typeName(iface.Name, "wl_", "")
				expectedSince := req.Since
				if expectedSince < 1 {
					expectedSince = 1
				}
				found := false
				for _, r := range d.Requests {
					if r.Name == reqName {
						found = true
						if r.Since != expectedSince {
							t.Errorf("%s.%s Since()=%d, XML since=%d", tname, req.Name, r.Since, expectedSince)
						}
						break
					}
				}
				if !found {
					t.Errorf("%s.%s: request not found in generated data", tname, req.Name)
				}
			}

			for _, evt := range iface.Events {
				evtName := pascal(evt.Name)
				expectedSince := evt.Since
				if expectedSince < 1 {
					expectedSince = 1
				}
				found := false
				for _, e := range d.Events {
					if e.Name == evtName {
						found = true
						if e.Since != expectedSince {
							t.Errorf("%s event %s Since()=%d, XML since=%d", iface.Name, evt.Name, e.Since, expectedSince)
						}
						break
					}
				}
				if !found {
					t.Errorf("%s event %s: not found in generated data", iface.Name, evt.Name)
				}
			}

			// 3. Enum entry values
			for _, en := range iface.Enums {
				enumName := pascal(en.Name)
				for _, dEnum := range d.Enums {
					if dEnum.Name == enumName {
						for _, entry := range en.Entries {
							entryName := pascal(entry.Name)
							expectedConst := dEnum.Type + entryName
							found := false
							for _, de := range dEnum.Entries {
								if de.Const == expectedConst {
									found = true
									expectedVal := fmt.Sprintf("%d", entry.Value)
									if de.Val != expectedVal {
										t.Errorf("enum %s.%s value=%s, XML=%s", dEnum.Type, entryName, de.Val, expectedVal)
									}
									break
								}
							}
							if !found {
								t.Errorf("enum entry %s not found in generated data", expectedConst)
							}
						}
					}
				}
			}

			// 4. new_id return types
			for _, req := range iface.Requests {
				for _, a := range req.Args {
					if a.Type == "new_id" && a.Interface != "" {
						expectedType := typeName(a.Interface, "wl_", "")
						found := false
						for _, r := range d.Requests {
							if r.Name == pascal(req.Name) {
								found = true
								if !r.HasNewID {
									t.Errorf("%s.%s: expected HasNewID for %s->%s", iface.Name, req.Name, a.Name, a.Interface)
								}
								if r.NewIDType != expectedType {
									t.Errorf("%s.%s: NewIDType=%q, want %q", iface.Name, req.Name, r.NewIDType, expectedType)
								}
								break
							}
						}
						if !found {
							t.Errorf("%s.%s: request not found for new_id check", iface.Name, req.Name)
						}
					}

				}
			}

			// 5. Destructor requests have matching entries
			for _, req := range iface.Requests {
				if req.Type == "destructor" {
					found := false
					for _, r := range d.Requests {
						if r.Name == pascal(req.Name) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%s.%s: destructor not found in generated data", iface.Name, req.Name)
					}
				}
			}
		})
	}
}

func TestCrossValidateAllProtocols(t *testing.T) {
	root := "../../wayland-protocols"
	tiers := []string{"stable", "staging", "unstable", "experimental"}
	var xmlFiles []string
	for _, tier := range tiers {
		_ = filepath.Walk(filepath.Join(root, tier), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, ".xml") {
				xmlFiles = append(xmlFiles, path)
			}
			return nil
		})
	}
	if len(xmlFiles) == 0 {
		t.Fatal("no XML protocol files found")
	}
	t.Logf("found %d protocol XML files", len(xmlFiles))

	for _, xmlPath := range xmlFiles {
		relPath, _ := filepath.Rel(root, xmlPath)
		t.Run(relPath, func(t *testing.T) {
			proto, err := Parse(xmlPath)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			prefix := autoPrefix(proto.Interfaces)

			cfg := GenerateConfig{
				Package:    "testpkg",
				TrimPrefix: prefix,
			}

			ifaceSet := make(map[string]bool, len(proto.Interfaces))
			for i := range proto.Interfaces {
				ifaceSet[proto.Interfaces[i].Name] = true
			}

			for _, iface := range proto.Interfaces {
				tname := typeName(iface.Name, prefix, "")
				if tname == "" {
					t.Errorf("%s: typeName is empty after trimming prefix %q", iface.Name, prefix)
					continue
				}
				r := []rune(tname)
				if len(r) > 0 && !unicode.IsLetter(r[0]) {
					t.Errorf("%s -> typeName %q starts with non-letter", iface.Name, tname)
					continue
				}

				d, err := buildData(&iface, tname, cfg, ifaceSet)
				if err != nil {
					t.Errorf("%s: buildData: %v", iface.Name, err)
					continue
				}

				for idx, req := range d.Requests {
					if req.Opcode != idx {
						t.Errorf("%s request %s opcode=%d, want %d", tname, req.Name, req.Opcode, idx)
					}
				}
				for idx, ev := range d.Events {
					if ev.Opcode != idx {
						t.Errorf("%s event %s opcode=%d, want %d", tname, ev.Name, ev.Opcode, idx)
					}
				}

				for _, en := range iface.Enums {
					enumName := pascal(en.Name)
					for _, dEnum := range d.Enums {
						if dEnum.Name == enumName {
							for _, entry := range en.Entries {
								entryName := pascal(entry.Name)
								expectedConst := dEnum.Type + entryName
								found := false
								for _, de := range dEnum.Entries {
									if de.Const == expectedConst {
										found = true
										expectedVal := fmt.Sprintf("%d", entry.Value)
										if de.Val != expectedVal {
											t.Errorf("enum %s.%s value=%s, XML=%s", dEnum.Type, entryName, de.Val, expectedVal)
										}
										break
									}
								}
								if !found {
									t.Errorf("enum entry %s not found in generated data", expectedConst)
								}
							}
						}
					}
				}

				for _, req := range iface.Requests {
					for _, a := range req.Args {
						if a.Type == "new_id" && a.Interface != "" {
							found := false
							for _, r := range d.Requests {
								if r.Name == pascal(req.Name) {
									found = true
									if ifaceSet[a.Interface] {
										expectedType := typeName(a.Interface, prefix, "")
										if !r.HasNewID {
											t.Errorf("%s.%s: expected HasNewID for %s->%s", tname, req.Name, a.Name, a.Interface)
										}
										if r.NewIDType != expectedType {
											t.Errorf("%s.%s: NewIDType=%q, want %q", tname, req.Name, r.NewIDType, expectedType)
										}
									} else {
										if !r.HasCrossNewID {
											t.Errorf("%s.%s: expected HasCrossNewID for cross-protocol %s->%s", tname, req.Name, a.Name, a.Interface)
										}
									}
									break
								}
							}
							if !found {
								t.Errorf("%s.%s: request not found for new_id check", tname, req.Name)
							}
						}
					}
				}

				for _, req := range iface.Requests {
					if req.Type == "destructor" {
						found := false
						for _, r := range d.Requests {
							if r.Name == pascal(req.Name) {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("%s.%s: destructor not found", tname, req.Name)
						}
					}
				}

				// Arg order validation
				for _, req := range iface.Requests {
					for _, r := range d.Requests {
						if r.Name != pascal(req.Name) {
							continue
						}
						xmlArgs := nonNewIDXMLArgs(req.Args)
						if len(r.Args) != len(xmlArgs) {
							t.Errorf("%s.%s arg count: xml=%d gen=%d",
								tname, req.Name, len(xmlArgs), len(r.Args))
						} else {
							for i := range xmlArgs {
								if r.Args[i].GoName != pascal(xmlArgs[i].Name) {
									t.Errorf("%s.%s arg[%d] name: xml=%s gen=%s",
										tname, req.Name, i, xmlArgs[i].Name, r.Args[i].GoName)
								}
							}
						}
						break
					}
				}
				for _, ev := range iface.Events {
					for _, e := range d.Events {
						if e.Name != pascal(ev.Name) {
							continue
						}
						if len(e.Args) != len(ev.Args) {
							t.Errorf("%s event %s arg count: xml=%d gen=%d", tname, ev.Name, len(ev.Args), len(e.Args))
						} else {
							for i := range ev.Args {
								if e.Args[i].GoName != pascal(ev.Args[i].Name) {
									t.Errorf("%s event %s arg[%d] name: xml=%s gen=%s",
										tname, ev.Name, i, ev.Args[i].Name, e.Args[i].GoName)
								}
							}
						}
						break
					}
				}
			}
		})
	}
}

func nonNewIDXMLArgs(args []Arg) []Arg {
	var out []Arg
	for _, a := range args {
		if a.Type == "new_id" && a.Interface == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}
