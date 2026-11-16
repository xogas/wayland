package wayland

// ArgType is the wire type of a message argument.
type ArgType uint8

// Wire argument types.
const (
	ArgInt ArgType = iota
	ArgUint
	ArgFixed
	ArgString
	ArgArray
	ArgFD
	ArgNewID
	ArgObject
)

// ArgSpec describes one argument in a request or event signature.
type ArgSpec struct {
	Name            string
	Type            ArgType
	Interface       string
	Enum            string
	AllowNull       bool
	Since           uint32
	DeprecatedSince uint32
}

// EnumEntry describes one value of a protocol enum.
type EnumEntry struct {
	Name            string
	Value           int64
	Since           uint32
	DeprecatedSince uint32
}

// Enum describes one protocol enum declared on an interface.
type Enum struct {
	Name     string
	Since    uint32
	Bitfield bool
	Entries  []EnumEntry
}

// Message describes one request or event.
type Message struct {
	Name            string
	Since           uint32
	DeprecatedSince uint32
	Destructor      bool
	Args            []ArgSpec
}

// Interface is the generated metadata for one protocol interface.
type Interface struct {
	Name     string
	Version  uint32
	Frozen   bool
	Requests []Message
	Events   []Message
	Enums    []Enum
}

// InterfaceByName returns generated metadata by wire name. Duplicate names
// resolve to the stable interface; the unstable duplicate is not returned.
func InterfaceByName(name string) (*Interface, bool) {
	iface, ok := generatedInterfaces[name]
	return iface, ok
}

// MustInterface returns generated metadata by wire name.
func MustInterface(name string) *Interface {
	return mustInterface(name)
}

func mustInterface(name string) *Interface {
	iface, ok := generatedInterfaces[name]
	if !ok {
		panic("wayland: missing generated interface " + name)
	}
	return iface
}
