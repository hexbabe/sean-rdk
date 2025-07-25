package ftdc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	v1 "go.viam.com/api/app/datasync/v1"
	"go.viam.com/utils"
	"go.viam.com/utils/rpc"

	"go.viam.com/rdk/logging"
)

type uploader struct {
	dataSyncClient v1.DataSyncServiceClient
	ftdcDir        string
	partID         string
	logger         logging.Logger

	toUpload chan string
	worker   *utils.StoppableWorkers
}

func newUploader(cloudConn rpc.ClientConn, ftdcDir, partID string, logger logging.Logger) *uploader {
	return &uploader{
		dataSyncClient: v1.NewDataSyncServiceClient(cloudConn),
		ftdcDir:        ftdcDir,
		partID:         partID,
		logger:         logger,
		toUpload:       make(chan string, 10),
	}
}

func (uploader *uploader) start() {
	uploader.worker = utils.NewBackgroundStoppableWorkers(uploader.uploadRunner)
}

func (uploader *uploader) stopAndJoin() {
	if uploader.worker != nil {
		uploader.worker.Stop()
	}
}

func (uploader *uploader) addFileToUpload(filename string) {
	select {
	case uploader.toUpload <- filename:
	default:
	}
}

func (uploader *uploader) uploadRunner(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ftdcFilename := <-uploader.toUpload:
			if err := uploader.uploadFile(ctx, ftdcFilename); err != nil {
				uploader.logger.Warnw("Error uploading FTDC file", "filename", ftdcFilename, "error", err)
			}
		}
	}
}

func (uploader *uploader) uploadFile(ctx context.Context, filename string) error {
	uploader.logger.Debugw("Uploading FTDC file", "filename", filename)
	binaryClient, err := uploader.dataSyncClient.FileUpload(ctx)
	if err != nil {
		return err
	}

	err = binaryClient.Send(&v1.FileUploadRequest{
		UploadPacket: &v1.FileUploadRequest_Metadata{
			Metadata: &v1.UploadMetadata{
				PartId:        uploader.partID,
				Type:          v1.DataType_DATA_TYPE_FILE,
				FileName:      filename,
				FileExtension: filepath.Ext(filename),
			},
		},
	})
	if err != nil {
		return err
	}

	file, err := os.Open(filename) //nolint: gosec
	if err != nil {
		return err
	}
	defer utils.UncheckedErrorFunc(file.Close)

	uploadBuf := make([]byte, 32*1024)
	for {
		bytesRead, err := file.Read(uploadBuf)
		if errors.Is(err, io.EOF) {
			break
		}

		if err = binaryClient.Send(&v1.FileUploadRequest{
			UploadPacket: &v1.FileUploadRequest_FileContents{
				FileContents: &v1.FileData{
					Data: uploadBuf[:bytesRead],
				},
			},
		}); err != nil {
			return err
		}
	}

	_, err = binaryClient.CloseAndRecv()
	return err
}
