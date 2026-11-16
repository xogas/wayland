package main

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

func (m *msg) Dynamic() bool {
	for _, a := range m.Args {
		if a.Dynamic {
			return true
		}
	}
	return false
}
