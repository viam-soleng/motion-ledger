// Package motion provides helpers for resolving and querying motion
// detector vision services in a uniform way.
package motion

import (
	"context"
	"fmt"
	"strings"

	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/vision"
)

// ResolvedDetector pairs a resolved vision service with the camera name
// it should be queried against.
type ResolvedDetector struct {
	Service vision.Service
	Camera  string
}

// ResolveConfiguredDetectors resolves the motion detectors named in the
// input map and pairs each with its configured camera name.
//
// Keys are vision-service resource names; values are camera names passed
// through to DetectionsFromCamera. Cameras are not declared as module
// dependencies — the vision service is responsible for resolving its own
// camera (which may live on a remote part).
func ResolveConfiguredDetectors(
	deps resource.Dependencies,
	detectorCameras map[string]string,
) (map[string]ResolvedDetector, error) {

	out := make(map[string]ResolvedDetector, len(detectorCameras))

	for name, camera := range detectorCameras {
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

		out[name] = ResolvedDetector{Service: v, Camera: camera}
	}

	return out, nil
}

// QueryMotion asks the vision service for detections from the configured
// camera and returns the confidence of the first "motion" label, if any.
func QueryMotion(
	ctx context.Context,
	detector vision.Service,
	camera string,
) (float64, error) {

	dets, err := detector.DetectionsFromCamera(ctx, camera, nil)
	if err != nil {
		return 0.0, err
	}

	for _, d := range dets {
		if strings.EqualFold(d.Label(), "motion") {
			return d.Score(), nil
		}
	}

	return 0.0, nil
}
