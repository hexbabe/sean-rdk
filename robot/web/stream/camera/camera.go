// Package camera provides utilities for working with camera resources in the context of streaming.
package camera

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"sync/atomic"
	"time"

	"github.com/pion/mediadevices/pkg/prop"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/rimage"
	"go.viam.com/rdk/robot"
	rutils "go.viam.com/rdk/utils"
)

// ttffLogger is a package-level logger for TTFF profiling. Set via SetTTFFLogger.
var ttffLogger logging.Logger = logging.NewLogger("ttff")

// SetTTFFLogger sets the logger used for TTFF profiling output.
func SetTTFFLogger(l logging.Logger) {
	ttffLogger = l
}

// StreamableImageMIMETypes represents all mime types the stream server supports.
// The order of the slice defines the priority of the mime types.
var StreamableImageMIMETypes = []string{
	rutils.MimeTypeJPEG,
	rutils.MimeTypePNG,
	rutils.MimeTypeRawRGBA,
	rutils.MimeTypeRawDepth,
	rutils.MimeTypeQOI,
}

// cropToEvenDimensions crops an image to even dimensions for x264 compatibility.
// x264 only supports even resolutions. This ensures all streamed images work with x264.
func cropToEvenDimensions(img image.Image) (image.Image, error) {
	if img, ok := img.(*rimage.LazyEncodedImage); ok {
		if err := img.DecodeConfig(); err != nil {
			return nil, err
		}
	}

	hasOddWidth := img.Bounds().Dx()%2 != 0
	hasOddHeight := img.Bounds().Dy()%2 != 0
	if !hasOddWidth && !hasOddHeight {
		return img, nil
	}

	rImg := rimage.ConvertImage(img)
	newWidth := rImg.Width()
	newHeight := rImg.Height()
	if hasOddWidth {
		newWidth--
	}
	if hasOddHeight {
		newHeight--
	}
	return rImg.SubImage(image.Rect(0, 0, newWidth, newHeight)), nil
}

// Camera returns the camera from the robot (derived from the stream) or
// an error if it has no camera.
func Camera(robot robot.Robot, stream gostream.Stream) (camera.Camera, error) {
	// Stream names are slightly modified versions of the resource short name
	shortName := stream.Name()
	cam, err := camera.FromProvider(robot, shortName)
	if err != nil {
		return nil, err
	}
	return cam, nil
}

// GetStreamableNamedImageFromCamera returns the first named image it finds from the camera that is supported for streaming.
// It prioritizes images based on the order of StreamableImageMIMETypes.
func GetStreamableNamedImageFromCamera(ctx context.Context, cam camera.Camera) (camera.NamedImage, error) {
	imagesStart := time.Now()
	namedImages, _, err := cam.Images(ctx, nil, nil)
	ttffLogger.Infow("[TTFF] cam.Images (unfiltered, source detection)",
		"camera", cam.Name(), "duration", time.Since(imagesStart), "imageCount", len(namedImages), "err", err)
	if err != nil {
		return camera.NamedImage{}, err
	}
	if len(namedImages) == 0 {
		return camera.NamedImage{}, fmt.Errorf("no images received for camera %q", cam.Name())
	}

	matchStart := time.Now()
	for _, streamableMimeType := range StreamableImageMIMETypes {
		for _, namedImage := range namedImages {
			if namedImage.MimeType() == streamableMimeType {
				ttffLogger.Infow("[TTFF] source mime match (direct)",
					"camera", cam.Name(), "sourceName", namedImage.SourceName,
					"mimeType", streamableMimeType, "matchDuration", time.Since(matchStart))
				return namedImage, nil
			}

			imgBytes, err := namedImage.Bytes(ctx)
			if err != nil {
				continue
			}

			decodeStart := time.Now()
			_, format, err := image.DecodeConfig(bytes.NewReader(imgBytes))
			ttffLogger.Infow("[TTFF] DecodeConfig fallback",
				"camera", cam.Name(), "sourceName", namedImage.SourceName,
				"format", format, "decodeDuration", time.Since(decodeStart), "err", err)
			if err != nil {
				continue
			}
			if rutils.FormatStringToMimeType(format) == streamableMimeType {
				ttffLogger.Infow("[TTFF] source mime match (DecodeConfig fallback)",
					"camera", cam.Name(), "sourceName", namedImage.SourceName,
					"mimeType", streamableMimeType, "matchDuration", time.Since(matchStart))
				return namedImage, nil
			}
		}
	}
	return camera.NamedImage{}, fmt.Errorf("no images were found with a streamable mime type for camera %q", cam.Name())
}

// getImageBySourceName retrieves a specific named image from the camera by source name.
func getImageBySourceName(ctx context.Context, cam camera.Camera, sourceName string) (camera.NamedImage, error) {
	filterSourceNames := []string{sourceName}
	namedImages, _, err := cam.Images(ctx, filterSourceNames, nil)
	if err != nil {
		return camera.NamedImage{}, err
	}

	switch len(namedImages) {
	case 0:
		return camera.NamedImage{}, fmt.Errorf("no images found for requested source name: %s", sourceName)
	case 1:
		namedImage := namedImages[0]
		if namedImage.SourceName != sourceName {
			return camera.NamedImage{}, fmt.Errorf("mismatched source name: requested %q, got %q", sourceName, namedImage.SourceName)
		}
		return namedImage, nil
	default:
		// At this point, multiple images were returned. This can happen if the camera is on an older version of the API and does not support
		// filtering by source name, or if there is a bug in the camera resource's filtering logic. In this unfortunate case, we'll match the
		// requested source name and tank the performance costs.
		responseSourceNames := []string{}
		for _, namedImage := range namedImages {
			if namedImage.SourceName == sourceName {
				return namedImage, nil
			}
			responseSourceNames = append(responseSourceNames, namedImage.SourceName)
		}
		return camera.NamedImage{},
			fmt.Errorf("no matching source name found for multiple returned images: requested %q, got %q", sourceName, responseSourceNames)
	}
}

// VideoSourceFromCamera converts a camera resource into a gostream VideoSource.
// This is useful for streaming video from a camera resource.
func VideoSourceFromCamera(ctx context.Context, cam camera.Camera, logger logging.Logger) (gostream.VideoSource, error) {
	// The reader callback uses a small state machine to determine which image to request from the camera.
	// A `sourceName` is used to track the selected image source. On the first call, `sourceName` is nil,
	// so the first available streamable image is chosen. On subsequent successful calls, the same `sourceName`
	// is used. If any errors occur while getting an image, `sourceName` is reset to nil, and the selection
	// process starts over on the next call. This allows the stream to recover if a source becomes unavailable.
	var sourceName *string
	var frameCount atomic.Int64
	var readerStart time.Time
	reader := gostream.VideoReaderFunc(func(ctx context.Context) (image.Image, func(), error) {
		frameNum := frameCount.Add(1)
		if frameNum == 1 {
			readerStart = time.Now()
			logger.Infow("[TTFF] VideoReader first call", "camera", cam.Name())
		}
		frameStart := time.Now()
		var respNamedImage camera.NamedImage

		if sourceName == nil {
			sourceDetectStart := time.Now()
			namedImage, err := GetStreamableNamedImageFromCamera(ctx, cam)
			if err != nil {
				return nil, func() {}, err
			}
			logger.Infow("[TTFF] source detection complete",
				"camera", cam.Name(), "sourceName", namedImage.SourceName,
				"duration", time.Since(sourceDetectStart))
			respNamedImage = namedImage
			sourceName = &namedImage.SourceName
		} else {
			var err error
			respNamedImage, err = getImageBySourceName(ctx, cam, *sourceName)
			if err != nil {
				sourceName = nil
				return nil, func() {}, err
			}
		}

		decodeStart := time.Now()
		img, err := respNamedImage.Image(ctx)
		if err != nil {
			sourceName = nil
			return nil, func() {}, err
		}

		img, err = cropToEvenDimensions(img)
		if err != nil {
			sourceName = nil
			return nil, func() {}, err
		}

		if frameNum <= 3 {
			logger.Infow("[TTFF] frame ready for encoder",
				"camera", cam.Name(), "frame", frameNum,
				"decodeDuration", time.Since(decodeStart),
				"totalFrameDuration", time.Since(frameStart),
				"timeSinceFirstRead", time.Since(readerStart),
				"width", img.Bounds().Dx(), "height", img.Bounds().Dy())
		}

		return img, func() {}, nil
	})

	// Return empty prop because there are no downstream consumers of the video props anyways.
	// The video encoder's actual properties are set by processInputFrames by sniffing a returned image.
	// We no longer ask the camera for an image in this code path to fill in video props because if the camera
	// hangs on this call, we can potentially block the resource reconfiguration due
	// to the tight coupling of refreshing streams and the resource graph.
	//
	// Blocking the resource reconfiguration is a known issue: https://viam.atlassian.net/browse/RSDK-12744
	//
	// If we ever start relying on the video props for other purposes, we should think of a way to set them
	// without blocking the resource reconfiguration.
	return gostream.NewVideoSource(reader, prop.Video{}), nil
}
