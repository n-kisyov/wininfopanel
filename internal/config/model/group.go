package model

// GroupItem contains other display items, moving and hiding them together.
//
// Children are positioned in profile coordinates, not relative to the group;
// the group's own X and Y act as a drag handle offset that shifts every child.
type GroupItem struct {
	ItemBase

	Items ItemList `json:"items,omitempty"`
}

// NewGroupItem returns an empty group.
func NewGroupItem(name string) *GroupItem {
	return &GroupItem{ItemBase: newItemBase(name)}
}

// Kind implements DisplayItem.
func (g *GroupItem) Kind() ItemKind { return KindGroup }

// Clone implements DisplayItem.
func (g *GroupItem) Clone() DisplayItem {
	c := *g
	c.Items = CloneAll(g.Items)
	return &c
}

// Bounds implements DisplayItem, returning the union of its children.
func (g *GroupItem) Bounds(ctx *EvalContext) Rect {
	if len(g.Items) == 0 {
		return RectFromSize(float64(g.X), float64(g.Y), Size{})
	}

	bounds := g.Items[0].Bounds(ctx)
	for _, child := range g.Items[1:] {
		bounds = bounds.Union(child.Bounds(ctx))
	}
	return bounds
}

// Add appends a child.
func (g *GroupItem) Add(item DisplayItem) { g.Items = append(g.Items, item) }

// Remove deletes the child with the given ID, reporting whether it was found.
func (g *GroupItem) Remove(id string) bool {
	for i, item := range g.Items {
		if item.Base().ID == id {
			g.Items = append(g.Items[:i], g.Items[i+1:]...)
			return true
		}
	}
	return false
}

// Translate shifts the group and every descendant by (dx, dy).
func (g *GroupItem) Translate(dx, dy int) {
	g.X += dx
	g.Y += dy
	for _, child := range g.Items {
		if sub, ok := child.(*GroupItem); ok {
			sub.Translate(dx, dy)
			continue
		}
		base := child.Base()
		base.X += dx
		base.Y += dy
	}
}
