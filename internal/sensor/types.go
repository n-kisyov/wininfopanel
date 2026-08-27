// Package sensor defines the value types shared by every sensor source
// (HWiNFO shared memory, the native hardware monitor, and plugins) and the
// lookup interface display items resolve against.
package sensor

// Source identifies which subsystem owns a sensor.
type Source string

const (
	// SourceHWiNFO reads HWiNFO's shared memory. Requires HWiNFO running with
	// Shared Memory Support enabled.
	SourceHWiNFO Source = "hwinfo"
	// SourceNative is the built-in hardware monitor, this project's
	// replacement for LibreHardwareMonitor.
	SourceNative Source = "native"
	// SourcePlugin is a value published by an out-of-process plugin.
	SourcePlugin Source = "plugin"
)

// ValueType selects which of a reading's tracked statistics to display.
type ValueType string

const (
	ValueNow ValueType = "now"
	ValueMin ValueType = "min"
	ValueMax ValueType = "max"
	ValueAvg ValueType = "avg"
)

// Reading is one sensor's current state.
//
// Text is set only for string-valued sensors, which plugins may publish; when
// it is non-empty it takes precedence over the numeric fields for display.
type Reading struct {
	Name string  `json:"name"`
	Unit string  `json:"unit"`
	Now  float64 `json:"now"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Avg  float64 `json:"avg"`
	Text string  `json:"text,omitempty"`
}

// Select returns the statistic named by t. An unrecognized value type yields
// the current reading, matching InfoPanel's default-case behavior.
func (r Reading) Select(t ValueType) float64 {
	switch t {
	case ValueMin:
		return r.Min
	case ValueMax:
		return r.Max
	case ValueAvg:
		return r.Avg
	default:
		return r.Now
	}
}

// Key uniquely identifies a sensor across all sources.
//
// The fields used depend on Source: HWiNFO sensors are addressed by the
// (RemoteIndex, ID, Instance, EntryID) tuple its shared memory exposes, while
// native and plugin sensors use a string path.
type Key struct {
	Source Source `json:"source"`

	// RemoteIndex selects an HWiNFO instance: -1 is the local machine, 0 and
	// above are remote connections.
	RemoteIndex int    `json:"remoteIndex,omitempty"`
	ID          uint32 `json:"id,omitempty"`
	Instance    uint32 `json:"instance,omitempty"`
	EntryID     uint32 `json:"entryId,omitempty"`

	// Path addresses native sensors ("cpu/0/temperature") and plugin sensors
	// ("plugin-id/container-id/entry-id").
	Path string `json:"path,omitempty"`
}

// Resolver looks up the current value of a sensor. Implementations must be
// safe for concurrent use: the render loop reads while pollers write.
type Resolver interface {
	Read(Key) (Reading, bool)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(Key) (Reading, bool)

// Read implements Resolver.
func (f ResolverFunc) Read(k Key) (Reading, bool) { return f(k) }

// NopResolver resolves nothing. Useful in tests and when rendering a profile
// with no sensor sources attached.
type NopResolver struct{}

// Read implements Resolver.
func (NopResolver) Read(Key) (Reading, bool) { return Reading{}, false }
