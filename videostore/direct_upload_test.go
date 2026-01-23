package videostore

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	syncpb "go.viam.com/api/app/datasync/v1"
	"go.viam.com/rdk/logging"
	rutils "go.viam.com/rdk/utils"
	"go.viam.com/test"
	"go.viam.com/utils/rpc"
)

type fakeDataSyncServer struct {
	syncpb.UnimplementedDataSyncServiceServer

	gotMeta     *syncpb.UploadMetadata
	gotAnyBytes bool
}

func (s *fakeDataSyncServer) FileUpload(stream syncpb.DataSyncService_FileUploadServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			// EOF ends the stream; respond.
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		switch pkt := req.GetUploadPacket().(type) {
		case *syncpb.FileUploadRequest_Metadata:
			s.gotMeta = pkt.Metadata
		case *syncpb.FileUploadRequest_FileContents:
			if len(pkt.FileContents.GetData()) > 0 {
				s.gotAnyBytes = true
			}
		}
	}
	return stream.SendAndClose(&syncpb.FileUploadResponse{BinaryDataId: "file-123"})
}

func TestDirectUploaderFileUploadSendsMetadataAndChunks(t *testing.T) {
	logger := logging.NewTestLogger(t)

	// Env vars required by newDirectUploader.
	t.Setenv(rutils.APIKeyEnvVar, "fake-api-key")
	t.Setenv(rutils.APIKeyIDEnvVar, "fake-api-key-id")
	t.Setenv(rutils.MachinePartIDEnvVar, "part-123")

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	t.Cleanup(func() { _ = listener.Close() })

	// The direct uploader dials with API-key credentials, which uses proto.rpc.v1.AuthService.
	// So we must configure the server with an API-key auth handler (and entity data loader).
	rpcServer, err := rpc.NewServer(
		logger,
		rpc.WithDisableMulticastDNS(),
		rpc.WithAuthHandler(rpc.CredentialsTypeAPIKey, rpc.AuthHandlerFunc(
			func(_ context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
			},
		)),
	)
	test.That(t, err, test.ShouldBeNil)
	t.Cleanup(func() { rpcServer.Stop() })

	fake := &fakeDataSyncServer{}
	err = rpcServer.RegisterServiceServer(context.Background(), &syncpb.DataSyncService_ServiceDesc, fake)
	test.That(t, err, test.ShouldBeNil)

	go rpcServer.Serve(listener)

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "clip.mp4")
	err = os.WriteFile(path, []byte("hello world"), 0o600)
	test.That(t, err, test.ShouldBeNil)

	cfg := &DirectUploadConfig{
		Enabled: true,
		BaseURL: "http://" + listener.Addr().String(),
	}
	uploader, err := newDirectUploader(cfg, logger)
	test.That(t, err, test.ShouldBeNil)
	defer uploader.Close()

	fileID, err := uploader.uploadFile(context.Background(), path, []string{"t1", "t2"}, []string{"d1"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, fileID, test.ShouldEqual, "file-123")

	test.That(t, fake.gotMeta, test.ShouldNotBeNil)
	test.That(t, fake.gotMeta.GetPartId(), test.ShouldEqual, "part-123")
	test.That(t, fake.gotMeta.GetType(), test.ShouldEqual, syncpb.DataType_DATA_TYPE_FILE)
	test.That(t, fake.gotMeta.GetFileExtension(), test.ShouldEqual, ".mp4")
	test.That(t, fake.gotMeta.GetTags(), test.ShouldResemble, []string{"t1", "t2"})
	test.That(t, fake.gotMeta.GetDatasetIds(), test.ShouldResemble, []string{"d1"})
	test.That(t, fake.gotAnyBytes, test.ShouldBeTrue)
	test.That(t, filepath.IsAbs(fake.gotMeta.GetFileName()), test.ShouldBeTrue)
}
