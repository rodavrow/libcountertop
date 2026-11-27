package doorbellview

import (
	"image"
	"testing"

	gogui "github.com/go-gui-org/go-gui/gui"
)

func TestReplacedAndPrunedPicturesLeaveTheRegistry(t *testing.T) {
	var p pictures
	p.keepImage("front", image.NewRGBA(image.Rect(0, 0, 4, 4)))
	first := p.shown["front"].key
	p.keepImage("front", image.NewRGBA(image.Rect(0, 0, 4, 4)))
	second := p.shown["front"].key
	if gogui.HasImage(first) {
		t.Errorf("the replaced picture %s is still registered", first)
	}
	if !gogui.HasImage(second) {
		t.Fatalf("the current picture %s is not registered", second)
	}
	p.prune("front", nil)
	if gogui.HasImage(second) {
		t.Errorf("the pruned picture %s is still registered", second)
	}
}
