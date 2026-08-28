package beada

// Model is the panel model byte reported in a GetPanelInfo response.
type Model byte

// Model identifiers, as the firmware reports them. The numbering is not
// sequential by model name -- it follows the order the models were released.
const (
	Model5  Model = 0
	Model7  Model = 1
	Model6  Model = 2
	Model3  Model = 3
	Model4  Model = 4
	Model5C Model = 10
	Model5T Model = 11
	Model7C Model = 12
	Model3C Model = 13
	Model4C Model = 14
	Model6C Model = 15
	Model6S Model = 16
	Model2  Model = 17
	Model2W Model = 18
	Model7S Model = 19
	Model5S Model = 20
	Model8  Model = 21
	Model11 Model = 22
	Model9  Model = 23
	ModelY  Model = 24
	ModelX  Model = 25
	ModelZ  Model = 26
)

// ModelInfo describes a panel's physical characteristics.
type ModelInfo struct {
	// Name is the model as printed on the device, e.g. "6P" or "5C".
	Name string `json:"name"`

	// Width and Height are the panel's pixel dimensions in its natural
	// orientation.
	Width  int `json:"width"`
	Height int `json:"height"`

	// WidthMM and HeightMM are its physical size, which the UI shows so a
	// user can tell two same-resolution models apart.
	WidthMM  int `json:"widthMm"`
	HeightMM int `json:"heightMm"`
}

// Models maps each model byte to its characteristics.
//
// Dimensions come from the panel database rather than from the firmware's
// reported resolution, because some models report their pre-rotation
// orientation and would render sideways if taken at their word.
var Models = map[Model]ModelInfo{
	Model2:  {Name: "2", Width: 480, Height: 480, WidthMM: 53, HeightMM: 53},
	Model2W: {Name: "2W", Width: 480, Height: 480, WidthMM: 70, HeightMM: 70},
	Model3:  {Name: "3", Width: 320, Height: 480, WidthMM: 40, HeightMM: 62},
	Model3C: {Name: "3C", Width: 480, Height: 320, WidthMM: 62, HeightMM: 40},
	Model4:  {Name: "4", Width: 480, Height: 800, WidthMM: 56, HeightMM: 94},
	Model4C: {Name: "4C", Width: 800, Height: 480, WidthMM: 94, HeightMM: 56},
	Model5:  {Name: "5", Width: 800, Height: 480, WidthMM: 108, HeightMM: 65},
	Model5C: {Name: "5C", Width: 800, Height: 480, WidthMM: 108, HeightMM: 65},
	Model5T: {Name: "5T", Width: 800, Height: 480, WidthMM: 108, HeightMM: 65},
	Model5S: {Name: "5S", Width: 480, Height: 854, WidthMM: 62, HeightMM: 110},
	Model6:  {Name: "6", Width: 480, Height: 1280, WidthMM: 60, HeightMM: 161},
	Model6C: {Name: "6C", Width: 1280, Height: 480, WidthMM: 161, HeightMM: 60},
	Model6S: {Name: "6S", Width: 1280, Height: 480, WidthMM: 161, HeightMM: 60},
	Model7C: {Name: "7C", Width: 800, Height: 480, WidthMM: 62, HeightMM: 110},
	Model7S: {Name: "7S", Width: 1280, Height: 400, WidthMM: 190, HeightMM: 59},
	Model8:  {Name: "8", Width: 480, Height: 1920, WidthMM: 54, HeightMM: 219},
	Model9:  {Name: "9", Width: 462, Height: 1920, WidthMM: 55, HeightMM: 226},
	Model11: {Name: "11", Width: 440, Height: 1920, WidthMM: 58, HeightMM: 253},
	ModelX:  {Name: "X", Width: 440, Height: 1920, WidthMM: 58, HeightMM: 253},
	ModelY:  {Name: "Y", Width: 480, Height: 1920, WidthMM: 54, HeightMM: 219},
	ModelZ:  {Name: "Z", Width: 462, Height: 1920, WidthMM: 55, HeightMM: 226},
}

// String returns the model's printed name, or its numeric value when unknown.
func (m Model) String() string {
	if info, ok := Models[m]; ok {
		return info.Name
	}
	return "unknown(" + itoa(byte(m)) + ")"
}

// itoa formats a byte without pulling in strconv for one call.
func itoa(v byte) string {
	if v == 0 {
		return "0"
	}

	var digits [3]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = '0' + v%10
		v /= 10
	}
	return string(digits[i:])
}
