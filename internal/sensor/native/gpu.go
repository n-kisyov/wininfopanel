package native

import (
	"context"
	"fmt"
	"strings"

	"github.com/yusufpapurcu/wmi"
)

// gpuCollector reports GPU info via WMI.
type gpuCollector struct{}

func newGPUCollector() *gpuCollector {
	return &gpuCollector{}
}

func (c *gpuCollector) name() string { return "gpu" }

type win32_VideoController struct {
	Name       string
	AdapterRAM uint32
}

func (c *gpuCollector) collect(_ context.Context, out *sampleSet) error {
	var controllers []win32_VideoController
	err := wmi.Query("SELECT Name, AdapterRAM FROM Win32_VideoController", &controllers)
	if err != nil {
		return fmt.Errorf("wmi query failed: %w", err)
	}

	const group = "GPU"
	for i, gpu := range controllers {
		name := strings.TrimSpace(gpu.Name)
		if name == "" {
			continue
		}

		out.addText(fmt.Sprintf("gpu/%d/name", i), group, fmt.Sprintf("GPU %d Name", i), name)

		if gpu.AdapterRAM > 0 {
			out.add(fmt.Sprintf("gpu/%d/memory/total", i), group, fmt.Sprintf("GPU %d Memory Total", i), typeOther, "GB", float64(gpu.AdapterRAM)/bytesPerGB)
		}
	}

	return nil
}
