package gostream

import (
	"context"
	"fmt"

	"go.viam.com/utils"

	"go.viam.com/rdk/logging"
)

// StreamVideoSource streams the given video source to the stream forever until context signals cancellation.
func StreamVideoSource(ctx context.Context, vs VideoSource, stream Stream, logger logging.Logger) error {
	return streamMediaSource(ctx, vs, stream, func(ctx context.Context, frameErr error) {
		logger.Debugw("error getting frame", "error", frameErr)
	}, stream.InputVideoFrames, logger)
}

// StreamAudioSource streams the given video source to the stream forever until context signals cancellation.
func StreamAudioSource(ctx context.Context, as AudioSource, stream Stream, logger logging.Logger) error {
	return streamMediaSource(ctx, as, stream, func(ctx context.Context, frameErr error) {
		logger.Debugw("error getting frame", "error", frameErr)
	}, stream.InputAudioChunks, logger)
}

// StreamVideoSourceWithErrorHandler streams the given video source to the stream forever
// until context signals cancellation, frame errors are sent via the error handler.
func StreamVideoSourceWithErrorHandler(
	ctx context.Context, vs VideoSource, stream Stream, errHandler ErrorHandler, logger logging.Logger,
) error {
	return streamMediaSource(ctx, vs, stream, errHandler, stream.InputVideoFrames, logger)
}

// StreamAudioSourceWithErrorHandler streams the given audio source to the stream forever
// until context signals cancellation, audio errors are sent via the error handler.
func StreamAudioSourceWithErrorHandler(
	ctx context.Context, as AudioSource, stream Stream, errHandler ErrorHandler, logger logging.Logger,
) error {
	return streamMediaSource(ctx, as, stream, errHandler, stream.InputAudioChunks, logger)
}

// streamMediaSource will stream a source of media forever to the stream until the given context tells it to cancel.
func streamMediaSource[T, U any](
	ctx context.Context,
	ms MediaSource[T],
	stream Stream,
	errHandler ErrorHandler,
	inputChan func(props U) (chan<- MediaReleasePair[T], error),
	logger logging.Logger,
) error {
	streamLoop := func() error {
		readyCh, readyCtx := stream.StreamingReady()
		select {
		case <-ctx.Done():
			logger.Info("streamMediaSource: context done")
			return ctx.Err()
		case <-readyCh:
		}
		var props U
		if provider, ok := ms.(MediaPropertyProvider[U]); ok {
			var err error
			props, err = provider.MediaProperties(ctx)
			if err != nil {
				logger.Debugw("no properties found for media; will assume empty", "error", err)
			}
		} else {
			logger.Debug("no properties found for media; will assume empty")
		}
		input, err := inputChan(props)
		if err != nil {
			return err
		}
		logger.Info("streamMediaSource: calling ms.Stream")
		mediaStream, err := ms.Stream(ctx, errHandler)
		if err != nil {
			return err
		}
		defer func() {
			logger.Info("streamMediaSource: closing mediaStream")
			utils.UncheckedError(mediaStream.Close(ctx))
			logger.Info("streamMediaSource: mediaStream closed")
		}()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-readyCtx.Done():
				return nil
			default:
			}
			fmt.Println("streamMediaSource: calling mediaStream.Next")
			media, release, err := mediaStream.Next(ctx)
			fmt.Println("streamMediaSource: mediaStream.Next returned")
			if err != nil {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-readyCtx.Done():
				return nil
			case input <- MediaReleasePair[T]{media, release}:
			}
		}
	}
	for {
		if err := streamLoop(); err != nil {
			logger.Info("streamMediaSource: streamLoop returned error")
			return err
		}
	}
}
