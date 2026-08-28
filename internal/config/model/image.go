package model

// ImageSource selects where an image item gets its pixels.
type ImageSource string

const (
	// ImageFile loads from disk, either an absolute path or one relative to
	// the profile's asset directory.
	ImageFile ImageSource = "file"
	// ImageURL fetches over HTTP.
	ImageURL ImageSource = "url"
	// ImageRTSP pulls frames from an RTSP video stream.
	ImageRTSP ImageSource = "rtsp"
)

// ImageItem draws a still image, animated GIF, or video frame.
type ImageItem struct {
	ItemBase

	Source ImageSource `json:"source"`

	// Path is the file location. When Relative is set it is resolved against
	// the profile's asset directory, which is what makes profiles portable.
	Path     string `json:"path,omitempty"`
	Relative bool   `json:"relative,omitempty"`

	// URL is the HTTP or RTSP source, depending on Source.
	URL string `json:"url,omitempty"`

	// Volume is the audio level for video sources, 0-100.
	Volume int `json:"volume,omitempty"`

	// Scale resizes the image as a percentage; Width and Height override it
	// with explicit dimensions when non-zero.
	Scale  int `json:"scale"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// Cache keeps decoded frames in memory; PersistentCache additionally
	// survives profile reloads.
	Cache           bool `json:"cache,omitempty"`
	PersistentCache bool `json:"persistentCache,omitempty"`

	// Layer tints the image with LayerColor, used to recolor icon sets.
	Layer      bool   `json:"layer,omitempty"`
	LayerColor string `json:"layerColor,omitempty"`

	// ShowPanel renders the profile's own output into this item, letting a
	// panel embed a view of itself.
	ShowPanel bool `json:"showPanel,omitempty"`

	// FlipX mirrors the image horizontally.
	FlipX bool `json:"flipX,omitempty"`
	// Opacity scales alpha, 0..1.
	Opacity float64 `json:"opacity,omitempty"`
}

// NewImageItem returns an image item reading from a file.
func NewImageItem(name string) *ImageItem {
	return &ImageItem{
		ItemBase:   newItemBase(name),
		Source:     ImageFile,
		Scale:      100,
		Cache:      true,
		LayerColor: "#77FFFFFF",
		Opacity:    1,
	}
}

// Kind implements DisplayItem.
func (i *ImageItem) Kind() ItemKind { return KindImage }

// Clone implements DisplayItem.
func (i *ImageItem) Clone() DisplayItem {
	c := *i
	return &c
}

// Bounds implements DisplayItem.
//
// Explicit dimensions win; otherwise the natural size is unknown until the
// image is decoded, so an unsized item reports a zero-extent box at its origin
// and the renderer substitutes the decoded size.
func (i *ImageItem) Bounds(*EvalContext) Rect {
	return RectFromSize(float64(i.X), float64(i.Y), Size{
		Width:  float64(i.Width),
		Height: float64(i.Height),
	})
}

// EffectiveOpacity clamps opacity, treating an unset value as fully opaque so
// profiles written before the field existed still render.
func (i *ImageItem) EffectiveOpacity() float64 {
	if i.Opacity <= 0 {
		return 1
	}
	if i.Opacity > 1 {
		return 1
	}
	return i.Opacity
}

// HTTPImageItem periodically refetches an image over HTTP.
type HTTPImageItem struct {
	ImageItem

	// RefreshInterval is how often to refetch, in seconds.
	RefreshInterval int `json:"refreshInterval"`
}

// NewHTTPImageItem returns an HTTP-backed image refreshed every 60 seconds.
func NewHTTPImageItem(name string) *HTTPImageItem {
	img := NewImageItem(name)
	img.Source = ImageURL
	return &HTTPImageItem{ImageItem: *img, RefreshInterval: 60}
}

// Kind implements DisplayItem.
func (h *HTTPImageItem) Kind() ItemKind { return KindHTTPImage }

// Clone implements DisplayItem.
func (h *HTTPImageItem) Clone() DisplayItem {
	c := *h
	return &c
}

// SensorImageItem draws an image published by a plugin through the shared
// image buffer, rather than one loaded from disk.
type SensorImageItem struct {
	ImageItem
	SensorBinding
}

// NewSensorImageItem returns a plugin-provided image item.
func NewSensorImageItem(name string) *SensorImageItem {
	return &SensorImageItem{
		ImageItem:     *NewImageItem(name),
		SensorBinding: NewSensorBinding(),
	}
}

// Kind implements DisplayItem.
func (s *SensorImageItem) Kind() ItemKind { return KindSensorImage }

// Clone implements DisplayItem.
func (s *SensorImageItem) Clone() DisplayItem {
	c := *s
	return &c
}

// GaugeItem picks a frame from an ordered image set based on a sensor value,
// producing an analog-style indicator.
type GaugeItem struct {
	ItemBase
	SensorBinding

	// Images are the gauge's frames, ordered from minimum to maximum.
	Images []*ImageItem `json:"images,omitempty"`

	MinValue float64 `json:"minValue"`
	MaxValue float64 `json:"maxValue"`

	// Scale resizes frames as a percentage; Width and Height override it.
	Scale  int `json:"scale"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// FlipX mirrors frames horizontally.
	FlipX bool `json:"flipX,omitempty"`

	// AnimationSpeed eases the needle toward its target in frames per second.
	// Zero snaps immediately.
	AnimationSpeed float64 `json:"animationSpeed,omitempty"`
}

// NewGaugeItem returns an empty gauge over a 0-100 range.
func NewGaugeItem(name string) *GaugeItem {
	return &GaugeItem{
		ItemBase:      newItemBase(name),
		SensorBinding: NewSensorBinding(),
		MinValue:      0,
		MaxValue:      100,
		Scale:         100,
	}
}

// Kind implements DisplayItem.
func (g *GaugeItem) Kind() ItemKind { return KindGauge }

// Clone implements DisplayItem.
func (g *GaugeItem) Clone() DisplayItem {
	c := *g
	c.Images = make([]*ImageItem, len(g.Images))
	for i, img := range g.Images {
		c.Images[i] = img.Clone().(*ImageItem)
	}
	return &c
}

// Bounds implements DisplayItem.
func (g *GaugeItem) Bounds(*EvalContext) Rect {
	return RectFromSize(float64(g.X), float64(g.Y), Size{
		Width:  float64(g.Width),
		Height: float64(g.Height),
	})
}

// FrameIndex returns the fractional position into the image set for the
// gauge's current value. The integer part selects a frame and the fraction is
// the cross-fade weight toward the next one.
//
// The second result is false when the gauge has no sensor or no frames.
func (g *GaugeItem) FrameIndex(ctx *EvalContext) (float64, bool) {
	if len(g.Images) == 0 {
		return 0, false
	}
	if len(g.Images) == 1 {
		return 0, true
	}

	n, ok := g.Normalized(ctx, g.MinValue, g.MaxValue)
	if !ok {
		return 0, false
	}
	return n * float64(len(g.Images)-1), true
}
