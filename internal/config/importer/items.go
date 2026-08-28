package importer

import (
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// Per-type conversions. Each builds a wininfopanel item from InfoPanel's
// permissive XML struct, applying the shared base and style helpers first and
// then whatever the type adds.

func convertText(source xmlItem) model.DisplayItem {
	item := model.NewTextItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyTextStyle(&item.TextStyle, source)
	return item
}

func convertClock(source xmlItem) model.DisplayItem {
	item := model.NewClockItem()
	applyBase(&item.ItemBase, source)
	applyTextStyle(&item.TextStyle, source)

	// The format notation differs entirely between the two; an untranslated
	// "hh:mm:ss tt" would render as those literal letters.
	if converted := convertDateFormat(source.Format); converted != "" {
		item.Format = converted
	}
	return item
}

func convertCalendar(source xmlItem) model.DisplayItem {
	item := model.NewCalendarItem()
	applyBase(&item.ItemBase, source)
	applyTextStyle(&item.TextStyle, source)

	if converted := convertDateFormat(source.Format); converted != "" {
		item.Format = converted
	}
	return item
}

func convertSensor(source xmlItem) model.DisplayItem {
	item := model.NewSensorItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyTextStyle(&item.TextStyle, source)
	applyBinding(&item.SensorBinding, source)

	item.Threshold1 = model.Threshold{Value: source.Threshold1, Color: source.Threshold1Color}
	item.Threshold2 = model.Threshold{Value: source.Threshold2, Color: source.Threshold2Color}

	item.ShowName = source.ShowName
	item.ShowUnit = source.ShowUnit
	item.OverrideUnit = source.OverrideUnit
	item.Unit = source.Unit
	item.OverridePrecision = source.OverridePrecision
	item.Precision = source.Precision
	item.ShowThousandsSeparator = source.ShowThousandsSeparator

	return item
}

func convertTable(source xmlItem) model.DisplayItem {
	item := model.NewTableItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyTextStyle(&item.TextStyle, source)
	applyBinding(&item.SensorBinding, source)

	item.Format = source.TableFormat
	item.MaxRows = source.MaxRows
	return item
}

// applyChartStyle copies the framing and range shared by chart types.
func applyChartStyle(style *model.ChartStyle, source xmlItem) {
	if source.Width > 0 {
		style.Width = source.Width
	}
	if source.Height > 0 {
		style.Height = source.Height
	}

	style.MinValue = source.MinValue
	style.MaxValue = source.MaxValue
	style.AutoValue = source.AutoValue
	style.FlipX = source.FlipX

	style.Frame = source.Frame
	if source.FrameColor != "" {
		style.FrameColor = source.FrameColor
	}
	style.Background = source.Background
	if source.BackgroundColor != "" {
		style.BackgroundColor = source.BackgroundColor
	}
	if source.Color != "" {
		style.Color = source.Color
	}
}

func convertGraph(source xmlItem) model.DisplayItem {
	graphType := model.GraphLine
	if strings.EqualFold(source.GraphType, "HISTOGRAM") {
		graphType = model.GraphHistogram
	}

	item := model.NewGraphItem(source.Name, graphType)
	applyBase(&item.ItemBase, source)
	applyChartStyle(&item.ChartStyle, source)
	applyBinding(&item.SensorBinding, source)

	if source.Thickness > 0 {
		item.Thickness = source.Thickness
	}
	if source.Step > 0 {
		item.Step = source.Step
	}
	item.Fill = source.Fill
	if source.FillColor != "" {
		item.FillColor = source.FillColor
	}
	return item
}

func convertBar(source xmlItem) model.DisplayItem {
	item := model.NewBarItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyChartStyle(&item.ChartStyle, source)
	applyBinding(&item.SensorBinding, source)

	item.CornerRadius = source.CornerRadius
	item.Gradient = source.Gradient
	if source.GradientColor != "" {
		item.GradientColor = source.GradientColor
	}
	return item
}

func convertDonut(source xmlItem) model.DisplayItem {
	item := model.NewDonutItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyChartStyle(&item.ChartStyle, source)
	applyBinding(&item.SensorBinding, source)

	if source.Thickness > 0 {
		item.Thickness = source.Thickness
	}
	if source.Span > 0 {
		item.Span = source.Span
	}
	return item
}

func convertGauge(source xmlItem) model.DisplayItem {
	item := model.NewGaugeItem(source.Name)
	applyBase(&item.ItemBase, source)
	applyBinding(&item.SensorBinding, source)

	item.MinValue = source.MinValue
	item.MaxValue = source.MaxValue
	if item.MaxValue <= item.MinValue {
		item.MaxValue = item.MinValue + 100
	}

	item.Width = source.Width
	item.Height = source.Height
	if source.Scale > 0 {
		item.Scale = source.Scale
	}
	item.FlipX = source.FlipX
	item.AnimationSpeed = source.AnimationSpeed

	for _, frame := range source.Images.Items {
		image, ok := convertImage(frame).(*model.ImageItem)
		if !ok {
			continue
		}
		item.Images = append(item.Images, image)
	}
	return item
}

// shapeTypes maps InfoPanel's shape names onto this project's.
//
// The names line up case-insensitively, so the map exists to reject unknown
// values rather than to rename known ones: an unrecognized shape becomes a
// rectangle, which is visible and editable rather than silently absent.
var shapeTypes = map[string]model.ShapeType{
	"rectangle":     model.ShapeRectangle,
	"capsule":       model.ShapeCapsule,
	"trapezoid":     model.ShapeTrapezoid,
	"parallelogram": model.ShapeParallelogram,
	"ellipse":       model.ShapeEllipse,
	"triangle":      model.ShapeTriangle,
	"pentagon":      model.ShapePentagon,
	"hexagon":       model.ShapeHexagon,
	"octagon":       model.ShapeOctagon,
	"star":          model.ShapeStar,
	"plus":          model.ShapePlus,
	"arrow":         model.ShapeArrow,
}

func convertShape(source xmlItem) model.DisplayItem {
	shapeType, ok := shapeTypes[strings.ToLower(source.GraphType)]
	if !ok {
		shapeType = model.ShapeRectangle
	}

	item := model.NewShapeItem(source.Name, shapeType)
	applyBase(&item.ItemBase, source)

	if source.Width > 0 {
		item.Width = source.Width
	}
	if source.Height > 0 {
		item.Height = source.Height
	}
	item.CornerRadius = source.CornerRadius

	item.Fill = source.Fill || source.Fill2
	if source.FillColor != "" {
		item.FillColor = source.FillColor
	} else if source.Color != "" {
		item.FillColor = source.Color
	}

	item.StrokeWidth = source.StrokeWidth
	item.Stroke = source.StrokeWidth > 0
	if source.StrokeColor != "" {
		item.StrokeColor = source.StrokeColor
	}

	item.Gradient = source.Gradient
	if source.GradientColor != "" {
		item.GradientColor = source.GradientColor
	}
	return item
}

// applyImage copies the fields shared by every image-backed item.
func applyImage(item *model.ImageItem, source xmlItem) {
	applyBase(&item.ItemBase, source)

	switch strings.ToUpper(source.GraphType) {
	case "URL":
		item.Source = model.ImageURL
		item.URL = source.HttpUrl
	case "RTSP":
		item.Source = model.ImageRTSP
		item.URL = source.RtspUrl
	default:
		item.Source = model.ImageFile
	}

	item.Path = source.FilePath
	item.Relative = source.RelativePath
	item.Volume = source.Volume
	if source.Scale > 0 {
		item.Scale = source.Scale
	}
	item.Width = source.Width
	item.Height = source.Height
	item.Cache = source.Cache
	item.PersistentCache = source.PersistentCache
	item.Layer = source.Layer
	if source.LayerColor != "" {
		item.LayerColor = source.LayerColor
	}
	item.ShowPanel = source.ShowPanel
	item.FlipX = source.FlipX
}

func convertImage(source xmlItem) model.DisplayItem {
	item := model.NewImageItem(source.Name)
	applyImage(item, source)
	return item
}

func convertHTTPImage(source xmlItem) model.DisplayItem {
	item := model.NewHTTPImageItem(source.Name)
	applyImage(&item.ImageItem, source)

	// An HTTP image is always URL-backed regardless of what the type element
	// happened to hold.
	item.Source = model.ImageURL
	if source.HttpUrl != "" {
		item.URL = source.HttpUrl
	}
	return item
}

func convertSensorImage(source xmlItem) model.DisplayItem {
	item := model.NewSensorImageItem(source.Name)
	applyImage(&item.ImageItem, source)
	applyBinding(&item.SensorBinding, source)
	return item
}
