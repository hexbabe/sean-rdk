package utils

// Make sure that all mime types are registered in rimage/image_file.go with the appropriate
// format registration name i.e. "vnd.viam.rgba" are trailing substrings of its corresponding
// MIME type e.g. "image/vnd.viam.rgba" in mime.go. This is crucial to make sure
// that our mime type handling is 1:1 with the registered formats.
const (
	// MimeTypeRawRGBA is for go's internal image.NRGBA. This uses the custom header as
	// explained in the comments for rimage.DecodeImage and rimage.EncodeImage.
	MimeTypeRawRGBA = "image/vnd.viam.rgba"

	// MimeTypeRawDepth is for depth images.
	MimeTypeRawDepth = "image/vnd.viam.dep"

	// MimeTypeJPEG is regular jpgs.
	MimeTypeJPEG = "image/jpeg"

	// MimeTypePNG is regular pngs.
	MimeTypePNG = "image/png"

	// MimeTypePCD is for .pcd pountcloud files.
	MimeTypePCD = "pointcloud/pcd"

	// MimeTypeQOI is for .qoi "Quite OK Image" for lossless, fast encoding/decoding.
	MimeTypeQOI = "image/qoi"

	// MimeTypeTabular used to indicate tabular data, this is used mainly for filtering data.
	MimeTypeTabular = "x-application/tabular"

	// MimeTypeDefault used if mimetype cannot be inferred.
	MimeTypeDefault = "application/octet-stream"

	// MimeTypeH264 used to indicate H264 frames.
	MimeTypeH264 = "video/h264"

	// MimeTypeVideoMp4 is used to indicate .mp4 video files.
	MimeTypeVideoMP4 = "video/mp4"
)
