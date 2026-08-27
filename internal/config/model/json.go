package model

import (
	"encoding/json"
	"fmt"
)

// Display items are stored as a flat object carrying a "kind" discriminator
// alongside the type's own fields:
//
//	{"kind":"Sensor","id":"...","name":"CPU","x":10, ...}
//
// Go cannot unmarshal into an interface, so ItemList mediates: it reads the
// discriminator, allocates the matching concrete type, and decodes into it.

// NewItemOfKind allocates a zero-valued display item of the given kind.
func NewItemOfKind(kind ItemKind) (DisplayItem, error) {
	switch kind {
	case KindText:
		return &TextItem{}, nil
	case KindSensor:
		return &SensorItem{}, nil
	case KindTable:
		return &TableItem{}, nil
	case KindClock:
		return &ClockItem{}, nil
	case KindCalendar:
		return &CalendarItem{}, nil
	case KindImage:
		return &ImageItem{}, nil
	case KindHTTPImage:
		return &HTTPImageItem{}, nil
	case KindSensorImage:
		return &SensorImageItem{}, nil
	case KindGraph:
		return &GraphItem{}, nil
	case KindBar:
		return &BarItem{}, nil
	case KindDonut:
		return &DonutItem{}, nil
	case KindGauge:
		return &GaugeItem{}, nil
	case KindShape:
		return &ShapeItem{}, nil
	case KindGroup:
		return &GroupItem{}, nil
	default:
		return nil, fmt.Errorf("unknown display item kind %q", kind)
	}
}

// MarshalItem encodes a display item with its kind discriminator.
func MarshalItem(item DisplayItem) ([]byte, error) {
	body, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("marshal %s item: %w", item.Kind(), err)
	}
	if len(body) < 2 || body[0] != '{' {
		return nil, fmt.Errorf("marshal %s item: expected a JSON object, got %s", item.Kind(), body)
	}

	kind, err := json.Marshal(item.Kind())
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(body)+len(kind)+9)
	out = append(out, `{"kind":`...)
	out = append(out, kind...)
	if len(body) > 2 {
		out = append(out, ',')
		out = append(out, body[1:]...) // splice in the type's own fields
	} else {
		out = append(out, '}')
	}
	return out, nil
}

// UnmarshalItem decodes a display item, dispatching on its kind.
func UnmarshalItem(data []byte) (DisplayItem, error) {
	var envelope struct {
		Kind ItemKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("read display item kind: %w", err)
	}
	if envelope.Kind == "" {
		return nil, fmt.Errorf("display item is missing its %q field", "kind")
	}

	item, err := NewItemOfKind(envelope.Kind)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, item); err != nil {
		return nil, fmt.Errorf("decode %s item: %w", envelope.Kind, err)
	}
	return item, nil
}

// ItemList is an ordered layout of display items that round-trips through
// JSON despite DisplayItem being an interface.
type ItemList []DisplayItem

// MarshalJSON implements json.Marshaler.
func (l ItemList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}

	parts := make([]json.RawMessage, len(l))
	for i, item := range l {
		encoded, err := MarshalItem(item)
		if err != nil {
			return nil, err
		}
		parts[i] = encoded
	}
	return json.Marshal(parts)
}

// UnmarshalJSON implements json.Unmarshaler.
func (l *ItemList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("read display item list: %w", err)
	}
	if raw == nil {
		*l = nil
		return nil
	}

	items := make(ItemList, 0, len(raw))
	for i, entry := range raw {
		item, err := UnmarshalItem(entry)
		if err != nil {
			return fmt.Errorf("display item %d: %w", i, err)
		}
		items = append(items, item)
	}
	*l = items
	return nil
}
