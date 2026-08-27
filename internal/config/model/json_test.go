package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// sampleItems returns one populated instance of every display item kind, so
// round-trip coverage cannot silently miss a type.
func sampleItems() ItemList {
	text := NewTextItem("Hello")
	text.Bold = true
	text.Glow = GlowSettings{Enabled: true, Radius: 4, Color: "#FF0000", BlendMode: "Screen"}

	sensorItem := NewSensorItem("CPU")
	sensorItem.Key = sensor.Key{Source: sensor.SourceHWiNFO, ID: 1, Instance: 2, EntryID: 3}
	sensorItem.Threshold1 = Threshold{Value: 70, Color: "#FFA500"}
	sensorItem.Threshold2 = Threshold{Value: 90, Color: "#FF0000"}

	table := NewTableItem("Processes")
	table.Format = "0:150|1:60"
	table.Key = sensor.Key{Source: sensor.SourcePlugin, Path: "p/c/e"}

	gauge := NewGaugeItem("Fan")
	gauge.Images = []*ImageItem{NewImageItem("frame0"), NewImageItem("frame1")}
	gauge.AnimationSpeed = 2.5

	group := NewGroupItem("Cluster")
	group.Add(NewShapeItem("Box", ShapeHexagon))
	group.Add(NewDonutItem("Load"))

	return ItemList{
		text,
		sensorItem,
		table,
		NewClockItem(),
		NewCalendarItem(),
		NewImageItem("Logo"),
		NewHTTPImageItem("Remote"),
		NewSensorImageItem("PluginImage"),
		NewGraphItem("History", GraphLine),
		NewBarItem("Usage"),
		NewDonutItem("Ring"),
		gauge,
		NewShapeItem("Star", ShapeStar),
		group,
	}
}

func TestItemListRoundTripsEveryKind(t *testing.T) {
	original := sampleItems()

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ItemList
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("decoded %d items, want %d", len(decoded), len(original))
	}
	for i := range original {
		if !reflect.DeepEqual(original[i], decoded[i]) {
			t.Errorf("item %d (%s) did not survive the round trip:\n got %#v\nwant %#v",
				i, original[i].Kind(), decoded[i], original[i])
		}
	}
}

func TestEveryKindIsConstructible(t *testing.T) {
	kinds := []ItemKind{
		KindText, KindSensor, KindTable, KindClock, KindCalendar,
		KindImage, KindHTTPImage, KindSensorImage,
		KindGraph, KindBar, KindDonut, KindGauge, KindShape, KindGroup,
	}
	for _, kind := range kinds {
		item, err := NewItemOfKind(kind)
		if err != nil {
			t.Errorf("NewItemOfKind(%q): %v", kind, err)
			continue
		}
		if item.Kind() != kind {
			t.Errorf("NewItemOfKind(%q) reports kind %q", kind, item.Kind())
		}
	}
}

func TestUnmarshalItemRejectsUnknownKind(t *testing.T) {
	if _, err := UnmarshalItem([]byte(`{"kind":"Hologram"}`)); err == nil {
		t.Error("expected an error for an unknown kind")
	}
}

func TestUnmarshalItemRejectsMissingKind(t *testing.T) {
	if _, err := UnmarshalItem([]byte(`{"name":"orphan"}`)); err == nil {
		t.Error("expected an error when kind is absent")
	}
}

func TestMarshalItemEmitsKindDiscriminator(t *testing.T) {
	encoded, err := MarshalItem(NewBarItem("Usage"))
	if err != nil {
		t.Fatalf("MarshalItem: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if fields["kind"] != string(KindBar) {
		t.Errorf("kind = %v, want %q", fields["kind"], KindBar)
	}
	if fields["name"] != "Usage" {
		t.Errorf("name = %v, want the item's own field to survive", fields["name"])
	}
}

func TestCloneAssignsFreshIDsThroughout(t *testing.T) {
	group := NewGroupItem("Cluster")
	child := NewTextItem("Inner")
	group.Add(child)

	clone := group.Clone().(*GroupItem)

	if clone.ID == group.ID {
		t.Error("clone reused the group's ID")
	}
	if len(clone.Items) != 1 {
		t.Fatalf("clone has %d children, want 1", len(clone.Items))
	}
	if clone.Items[0].Base().ID == child.ID {
		t.Error("clone reused the child's ID")
	}
	if clone.Items[0].Base().Name != "Inner" {
		t.Error("clone lost the child's name")
	}
}

func TestGaugeCloneCopiesFrames(t *testing.T) {
	gauge := NewGaugeItem("Fan")
	gauge.Images = []*ImageItem{NewImageItem("a"), NewImageItem("b")}

	clone := gauge.Clone().(*GaugeItem)
	clone.Images[0].Name = "changed"

	if gauge.Images[0].Name != "a" {
		t.Error("mutating the clone's frame affected the original: frames were shared, not copied")
	}
}
