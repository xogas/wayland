package wayland

// Binder is implemented by objects that can bind a Wayland protocol interface
// to a server-side global. The generated BindXxx functions accept a Binder
// so they can be used with any client-side proxy that supports binding.
type Binder interface {
	Bind(name uint32, iface string, version uint32) (*Proxy, error)
}
