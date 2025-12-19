// Package motion provides helpers for resolving and querying motion
// detector vision services in a uniform way.
package motion

import (
	"image"
	"image/color"
	"context"
	"fmt"
	"strings"

	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/vision"
)

// emptyImage returns a minimal placeholder image.
// Some vision services require an image argument even when
// operating on internal camera state.
func emptyImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black)
	return img
}

// ResolveConfiguredDetectors resolves only the motion detectors
// explicitly listed in config.
func ResolveConfiguredDetectors(
	deps resource.Dependencies,
	names []string,
) (map[string]vision.Service, error) {

	out := make(map[string]vision.Service)

	for _, name := range names {
		rn := resource.Name{
			API:  vision.API,
			Name: name,
		}

		dep, ok := deps[rn]
		if !ok {
			return nil, fmt.Errorf("motion detector %q not found", name)
		}

		v, ok := dep.(vision.Service)
		if !ok {
			return nil, fmt.Errorf("%q is not a vision service", name)
		}

		out[name] = v
	}

	return out, nil
}

// QueryMotion queries a motion detector and returns the confidence
// of the "motion" label if present.
func QueryMotion(
	ctx context.Context,
	detector vision.Service,
) (float64, error) {
	
	// Use a placeholder image; detector is expected to operate on
	// its internally configured camera.
	img := emptyImage()
	dets, err := detector.Detections(ctx, img, nil)
	if err != nil {
		return 0.0, err
	}

	// Motion detectors are expected to emit a single "motion" detection,
	// but we defensively scan all detections.
	for _, d := range dets {
		if strings.ToLower(d.Label()) == "motion" {
			return d.Score(), nil
		}
	}

	return 0.0, nil
}
