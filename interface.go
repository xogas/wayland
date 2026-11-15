package wayland

// ArgType is the wire type of a message argument.
type ArgType uint8

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
	Name      string
	Type      ArgType
	Interface string
	Enum      string
	AllowNull bool
}

// Message describes one request or event.
type Message struct {
	Name  string
	Since uint32
	Args  []ArgSpec
}

// Interface is the generated metadata for one protocol interface.
type Interface struct {
	Name     string
	Version  uint32
	Requests []Message
	Events   []Message
}

// InterfaceByName returns generated metadata by wire name.
func InterfaceByName(name string) (*Interface, bool) {
	iface, ok := generatedInterfaces[name]
	return iface, ok
}

func mustInterface(name string) *Interface {
	iface, ok := generatedInterfaces[name]
	if !ok {
		panic("wayland: missing generated interface " + name)
	}
	return iface
}
