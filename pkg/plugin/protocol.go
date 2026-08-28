// Package plugin is the SDK for writing wininfopanel plugins.
//
// A plugin is an ordinary executable. wininfopanel discovers it, starts it,
// and talks to it over a named pipe; the plugin publishes sensors, text
// values, tables, and rendered images, which then appear as display items
// alongside HWiNFO's and the built-in monitor's.
//
// Running plugins out of process means a misbehaving one cannot crash
// wininfopanel, and that a plugin can be written in any language that can
// speak the protocol below. The Go SDK in this package handles all of it:
//
//	func main() {
//	    plugin.Serve(&MyPlugin{})
//	}
package plugin

import (
	"encoding/json"
	"time"
)

// ProtocolVersion is the wire format's version.
//
// The host sends it on connect and refuses a plugin that answers with a
// different one, rather than letting a mismatch surface later as a confusing
// decode failure.
const ProtocolVersion = 1

// PipeEnvVar names the environment variable carrying the pipe to connect to.
//
// It is passed through the environment rather than as an argument so a plugin
// can also take command-line flags of its own without colliding.
const PipeEnvVar = "WININFOPANEL_PIPE"

// Message is one frame in either direction.
//
// A single envelope type, rather than separate request and response types,
// keeps the decoder simple: read a message, look at Kind, dispatch.
type Message struct {
	// ID correlates a response with its request. Notifications leave it empty.
	ID string `json:"id,omitempty"`
	// Kind names the request, response, or notification.
	Kind Kind `json:"kind"`
	// Payload is the kind-specific body.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Error carries a failure in place of a payload.
	Error string `json:"error,omitempty"`
}

// Kind identifies what a message carries.
type Kind string

const (
	// KindHello is the host's opening request; the plugin answers with its
	// metadata and protocol version.
	KindHello Kind = "hello"
	// KindLoad asks the plugin for its containers and entries.
	KindLoad Kind = "load"
	// KindUpdate asks the plugin to refresh its values.
	KindUpdate Kind = "update"
	// KindAction invokes a named action.
	KindAction Kind = "action"
	// KindGetConfig requests the plugin's configuration properties.
	KindGetConfig Kind = "getConfig"
	// KindSetConfig applies one configuration value.
	KindSetConfig Kind = "setConfig"
	// KindShutdown asks the plugin to stop.
	KindShutdown Kind = "shutdown"

	// KindResponse answers a request, correlated by ID.
	KindResponse Kind = "response"

	// KindValues is a plugin-initiated push of changed values.
	KindValues Kind = "values"
	// KindLog is a plugin-initiated diagnostic message.
	KindLog Kind = "log"
	// KindImageResized tells the host an image buffer was replaced and must be
	// re-mapped.
	KindImageResized Kind = "imageResized"
)

// HelloResponse is the plugin's answer to KindHello.
type HelloResponse struct {
	ProtocolVersion int `json:"protocolVersion"`

	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	Website     string `json:"website,omitempty"`

	// UpdateIntervalMS is how often the plugin wants Update called.
	UpdateIntervalMS int `json:"updateIntervalMs"`

	// Actions the plugin exposes as buttons.
	Actions []ActionInfo `json:"actions,omitempty"`
	// Images the plugin renders, declared up front so the host can create
	// their shared buffers before the first update.
	Images []ImageInfo `json:"images,omitempty"`
	// Configurable reports whether the plugin has settings to edit.
	Configurable bool `json:"configurable,omitempty"`
}

// ActionInfo describes an action a plugin exposes.
type ActionInfo struct {
	// Name identifies the action on the wire.
	Name string `json:"name"`
	// DisplayName labels its button in the UI.
	DisplayName string `json:"displayName"`
}

// ImageInfo declares an image the plugin renders.
type ImageInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// LoadResponse is the plugin's answer to KindLoad.
type LoadResponse struct {
	Containers []Container `json:"containers"`
}

// Container groups related entries, appearing as one section in the UI.
type Container struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// EntryType distinguishes what an entry carries.
type EntryType string

const (
	// EntrySensor is a numeric reading with statistics.
	EntrySensor EntryType = "sensor"
	// EntryText is a string value.
	EntryText EntryType = "text"
	// EntryTable is tabular data.
	EntryTable EntryType = "table"
)

// Entry is one published value.
type Entry struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Type EntryType `json:"type"`
	Unit string    `json:"unit,omitempty"`

	// Value carries a sensor's current reading.
	Value float64 `json:"value,omitempty"`
	// Min, Max, and Avg are tracked by the SDK for sensors.
	Min float64 `json:"min,omitempty"`
	Max float64 `json:"max,omitempty"`
	Avg float64 `json:"avg,omitempty"`

	// Text carries a text entry's value.
	Text string `json:"text,omitempty"`

	// Table carries a table entry's contents.
	Table *Table `json:"table,omitempty"`
}

// Table is tabular data published by a plugin.
type Table struct {
	// Columns names each column, in order.
	Columns []string `json:"columns"`
	// Rows holds the cell text, row-major.
	Rows [][]string `json:"rows"`
	// Format is the default column layout, in "index:width|index:width" form.
	Format string `json:"format,omitempty"`
}

// ValuesNotification is a plugin-initiated push of changed entries.
//
// Only entries whose value changed are sent. A panel showing a handful of
// sensors should not pay for a plugin that publishes hundreds.
type ValuesNotification struct {
	Entries []EntryUpdate `json:"entries"`
}

// EntryUpdate is one changed value.
type EntryUpdate struct {
	// Path addresses the entry as "containerID/entryID".
	Path string `json:"path"`

	Value float64 `json:"value,omitempty"`
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
	Avg   float64 `json:"avg,omitempty"`
	Text  string  `json:"text,omitempty"`
	Table *Table  `json:"table,omitempty"`
}

// ActionRequest names the action to invoke.
type ActionRequest struct {
	Name string `json:"name"`
}

// ConfigType is the editor a configuration property needs.
type ConfigType string

const (
	ConfigString  ConfigType = "string"
	ConfigInteger ConfigType = "integer"
	ConfigDouble  ConfigType = "double"
	ConfigBoolean ConfigType = "boolean"
	ConfigChoice  ConfigType = "choice"
)

// ConfigProperty is one editable plugin setting.
type ConfigProperty struct {
	Key         string     `json:"key"`
	DisplayName string     `json:"displayName"`
	Type        ConfigType `json:"type"`
	Value       any        `json:"value"`

	// Min, Max, and Step bound the numeric editors.
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`

	// Options lists the choices for ConfigChoice.
	Options []string `json:"options,omitempty"`

	// Secret hides the value in the UI and keeps it out of logs, for API keys
	// and similar.
	Secret bool `json:"secret,omitempty"`
}

// ConfigResponse carries the plugin's current configuration.
type ConfigResponse struct {
	Properties []ConfigProperty `json:"properties"`
}

// SetConfigRequest applies one configuration value.
type SetConfigRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// LogNotification is a diagnostic message from a plugin, merged into
// wininfopanel's own log so a plugin's problems are visible in one place.
type LogNotification struct {
	// Level is "debug", "info", "warn", or "error".
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ImageResizedNotification reports that an image buffer was replaced.
type ImageResizedNotification struct {
	ImageID string `json:"imageId"`
	// Name is the new shared-memory section to map.
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// DefaultUpdateInterval is used when a plugin does not ask for one.
const DefaultUpdateInterval = time.Second

// MinUpdateInterval is the floor the host enforces, so a plugin cannot spin
// the host by asking to be updated continuously.
const MinUpdateInterval = 100 * time.Millisecond
