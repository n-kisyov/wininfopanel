package registry

import (
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

func fixed(value float64) sensor.Resolver {
	return sensor.ResolverFunc(func(sensor.Key) (sensor.Reading, bool) {
		return sensor.Reading{Now: value}, true
	})
}

func TestRegistryRoutesBySource(t *testing.T) {
	r := New()
	r.Register(sensor.SourceHWiNFO, fixed(1))
	r.Register(sensor.SourceNative, fixed(2))

	tests := []struct {
		source sensor.Source
		want   float64
	}{
		{sensor.SourceHWiNFO, 1},
		{sensor.SourceNative, 2},
	}
	for _, tt := range tests {
		reading, ok := r.Read(sensor.Key{Source: tt.source, Path: "x"})
		if !ok {
			t.Fatalf("source %q did not resolve", tt.source)
		}
		if reading.Now != tt.want {
			t.Errorf("source %q read %v, want %v", tt.source, reading.Now, tt.want)
		}
	}
}

func TestRegistryReportsUnregisteredSource(t *testing.T) {
	r := New()
	if _, ok := r.Read(sensor.Key{Source: sensor.SourcePlugin, Path: "p/c/e"}); ok {
		t.Error("an unregistered source resolved")
	}
}

func TestUnregisterStopsResolving(t *testing.T) {
	// A plugin that dies must make its sensors unavailable rather than leave
	// stale values on screen.
	r := New()
	r.Register(sensor.SourcePlugin, fixed(42))

	if _, ok := r.Read(sensor.Key{Source: sensor.SourcePlugin}); !ok {
		t.Fatal("registered source did not resolve")
	}

	r.Unregister(sensor.SourcePlugin)
	if _, ok := r.Read(sensor.Key{Source: sensor.SourcePlugin}); ok {
		t.Error("source still resolved after Unregister")
	}
}

func TestRegisterReplacesExistingResolver(t *testing.T) {
	r := New()
	r.Register(sensor.SourceNative, fixed(1))
	r.Register(sensor.SourceNative, fixed(2))

	reading, _ := r.Read(sensor.Key{Source: sensor.SourceNative})
	if reading.Now != 2 {
		t.Errorf("read %v, want the replacement resolver's 2", reading.Now)
	}
}

func TestHasAndSources(t *testing.T) {
	r := New()
	if r.Has(sensor.SourceNative) {
		t.Error("empty registry reported a source")
	}

	r.Register(sensor.SourceNative, fixed(1))
	if !r.Has(sensor.SourceNative) {
		t.Error("Has did not see a registered source")
	}
	if got := r.Sources(); len(got) != 1 || got[0] != sensor.SourceNative {
		t.Errorf("Sources() = %v, want [native]", got)
	}
}
