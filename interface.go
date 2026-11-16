package wayland

// ArgType is a wire argument type.
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

// ArgSpec is one request/event argument.
type ArgSpec struct {
	Name            string
	Type            ArgType
	Interface       string
	Enum            string
	AllowNull       bool
	Since           uint32
	DeprecatedSince uint32
}

// EnumEntry is one enum value.
type EnumEntry struct {
	Name            string
	Value           int64
	Since           uint32
	DeprecatedSince uint32
}

// Enum is one interface enum.
type Enum struct {
	Name     string
	Since    uint32
	Bitfield bool
	Entries  []EnumEntry
}

// Message is one request or event.
type Message struct {
	Name            string
	Since           uint32
	DeprecatedSince uint32
	Destructor      bool
	Args            []ArgSpec
}

// Interface is generated protocol metadata.
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

// MustInterface returns metadata or panics.
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
