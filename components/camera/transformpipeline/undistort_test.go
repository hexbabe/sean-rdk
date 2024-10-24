package transformpipeline

import (
	"context"
	"errors"
	"testing"

	"go.viam.com/test"
	"go.viam.com/utils/artifact"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/components/camera/fake"
	"go.viam.com/rdk/rimage"
	"go.viam.com/rdk/rimage/transform"
	"go.viam.com/rdk/utils"
)

// not the real intrinsic parameters of the image, only for testing purposes.
var undistortTestParams = &transform.PinholeCameraIntrinsics{
	Width:  128,
	Height: 72,
	Fx:     1.,
	Fy:     1.,
	Ppx:    0.,
	Ppy:    0.,
}

var undistortTestBC = &transform.BrownConrady{
	RadialK1:     0.,
	RadialK2:     0.,
	TangentialP1: 0.,
	TangentialP2: 0.,
	RadialK3:     0.,
}

func TestUndistortSetup(t *testing.T) {
	img, err := rimage.NewImageFromFile(artifact.MustPath("rimage/board1_small.png"))
	test.That(t, err, test.ShouldBeNil)
	source, err := camera.NewVideoSourceFromReader(context.Background(), &fake.StaticSource{ColorImg: img}, nil, camera.UnspecifiedStream)
	test.That(t, err, test.ShouldBeNil)

	// no camera parameters
	am := utils.AttributeMap{}
	_, _, err = newUndistortTransform(context.Background(), source, camera.ColorStream, am)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, errors.Is(err, transform.ErrNoIntrinsics), test.ShouldBeTrue)
	test.That(t, source.Close(context.Background()), test.ShouldBeNil)

	// bad stream type
	source, err = camera.NewVideoSourceFromReader(context.Background(), &fake.StaticSource{ColorImg: img}, nil, camera.UnspecifiedStream)
	test.That(t, err, test.ShouldBeNil)
	am = utils.AttributeMap{"intrinsic_parameters": undistortTestParams, "distortion_parameters": undistortTestBC}
	us, _, err := newUndistortTransform(context.Background(), source, camera.ImageType("fake"), am)
	test.That(t, err, test.ShouldBeNil)
	_, _, err = us.GetImage(context.Background())
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "do not know how to decode stream")
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)

	// success - conf has camera parameters
	us, stream, err := newUndistortTransform(context.Background(), source, camera.ColorStream, am)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, stream, test.ShouldEqual, camera.ColorStream)
	_, _, err = us.GetImage(context.Background())
	test.That(t, err, test.ShouldBeNil)

	test.That(t, us.Close(context.Background()), test.ShouldBeNil)
	test.That(t, source.Close(context.Background()), test.ShouldBeNil)
}

func TestUndistortImage(t *testing.T) {
	img, err := rimage.NewImageFromFile(artifact.MustPath("rimage/board1_small.png"))
	test.That(t, err, test.ShouldBeNil)
	source, err := camera.NewVideoSourceFromReader(context.Background(), &fake.StaticSource{ColorImg: img}, nil, camera.UnspecifiedStream)
	test.That(t, err, test.ShouldBeNil)

	// success
	am := utils.AttributeMap{"intrinsic_parameters": undistortTestParams, "distortion_parameters": undistortTestBC}
	us, stream, err := newUndistortTransform(context.Background(), source, camera.ColorStream, am)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, stream, test.ShouldEqual, camera.ColorStream)
	corrected, _, err := us.GetImage(context.Background())
	test.That(t, err, test.ShouldBeNil)
	result, ok := corrected.(*rimage.Image)
	test.That(t, ok, test.ShouldEqual, true)
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)

	sourceImg, _, err := source.GetImage(context.Background())
	test.That(t, err, test.ShouldBeNil)
	expected, ok := sourceImg.(*rimage.Image)
	test.That(t, ok, test.ShouldEqual, true)

	test.That(t, result, test.ShouldResemble, expected)

	// bad source
	source, err = camera.NewVideoSourceFromReader(
		context.Background(),
		&fake.StaticSource{ColorImg: rimage.NewImage(10, 10)},
		nil,
		camera.UnspecifiedStream,
	)
	test.That(t, err, test.ShouldBeNil)
	us, stream, err = newUndistortTransform(context.Background(), source, camera.ColorStream, am)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, stream, test.ShouldEqual, camera.ColorStream)
	_, _, err = us.GetImage(context.Background())
	test.That(t, err.Error(), test.ShouldContainSubstring, "img dimension and intrinsics don't match")
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)
}

func TestUndistortDepthMap(t *testing.T) {
	img, err := rimage.NewDepthMapFromFile(
		context.Background(), artifact.MustPath("rimage/board1_gray_small.png"))
	test.That(t, err, test.ShouldBeNil)
	source, err := camera.NewVideoSourceFromReader(context.Background(), &fake.StaticSource{DepthImg: img}, nil, camera.UnspecifiedStream)
	test.That(t, err, test.ShouldBeNil)

	// success
	am := utils.AttributeMap{"intrinsic_parameters": undistortTestParams, "distortion_parameters": undistortTestBC}
	us, stream, err := newUndistortTransform(context.Background(), source, camera.DepthStream, am)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, stream, test.ShouldEqual, camera.DepthStream)
	corrected, _, err := us.GetImage(context.Background())
	test.That(t, err, test.ShouldBeNil)
	result, ok := corrected.(*rimage.DepthMap)
	test.That(t, ok, test.ShouldEqual, true)
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)

	sourceImg, _, err := source.GetImage(context.Background())
	test.That(t, err, test.ShouldBeNil)
	expected, ok := sourceImg.(*rimage.DepthMap)
	test.That(t, ok, test.ShouldEqual, true)

	test.That(t, result, test.ShouldResemble, expected)

	// bad source
	source, err = camera.NewVideoSourceFromReader(
		context.Background(),
		&fake.StaticSource{DepthImg: rimage.NewEmptyDepthMap(10, 10)},
		nil,
		camera.UnspecifiedStream,
	)
	test.That(t, err, test.ShouldBeNil)
	us, stream, err = newUndistortTransform(context.Background(), source, camera.DepthStream, am)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, stream, test.ShouldEqual, camera.DepthStream)
	_, _, err = us.GetImage(context.Background())
	test.That(t, err.Error(), test.ShouldContainSubstring, "img dimension and intrinsics don't match")
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)

	// can't convert image to depth map
	source, err = camera.NewVideoSourceFromReader(
		context.Background(),
		&fake.StaticSource{ColorImg: rimage.NewImage(10, 10)},
		nil,
		camera.UnspecifiedStream,
	)
	test.That(t, err, test.ShouldBeNil)
	us, stream, err = newUndistortTransform(context.Background(), source, camera.DepthStream, am)
	test.That(t, stream, test.ShouldEqual, camera.DepthStream)
	test.That(t, err, test.ShouldBeNil)
	_, _, err = us.GetImage(context.Background())
	test.That(t, err.Error(), test.ShouldContainSubstring, "don't know how to make DepthMap")
	test.That(t, us.Close(context.Background()), test.ShouldBeNil)
}
