package videostore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	syncpb "go.viam.com/api/app/datasync/v1"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/utils"
	"go.viam.com/utils/rpc"
)

const (
	directUploadChunkSize = 64 * 1024
	maxMetadataTagLength  = 128
)

// directUploader implements optional "direct upload" behavior for videostore Save outputs.
//
// When enabled, videostore writes Save outputs to a staging directory (not a datamanager watched path),
// then uploads the file asynchronously to Viam App using the DataSync FileUpload streaming RPC.
//
// The upload runs with a small bounded retry loop with exponential backoff and can optionally delete the staged file
// after successful upload.
type directUploader struct {
	logger logging.Logger
	cfg    *DirectUploadConfig

	baseURL  string
	partID   string
	apiKey   string
	apiKeyID string

	mu     sync.Mutex
	conn   rpc.ClientConn
	client syncpb.DataSyncServiceClient
}

// newDirectUploader initializes a DataSync FileUpload client that authenticates via env-based API key credentials.
func newDirectUploader(cfg *DirectUploadConfig, logger logging.Logger) (*directUploader, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil //nolint:nilnil // nil is valid when direct upload is disabled
	}

	apiKey := os.Getenv(utils.APIKeyEnvVar)
	apiKeyID := os.Getenv(utils.APIKeyIDEnvVar)
	partID := os.Getenv(utils.MachinePartIDEnvVar)
	if apiKey == "" || apiKeyID == "" || partID == "" {
		return nil, fmt.Errorf(
			"direct_upload enabled but missing env vars: %s, %s, and/or %s",
			utils.APIKeyEnvVar,
			utils.APIKeyIDEnvVar,
			utils.MachinePartIDEnvVar,
		)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://app.viam.com"
	}

	return &directUploader{
		logger:   logger,
		cfg:      cfg,
		baseURL:  baseURL,
		partID:   partID,
		apiKey:   apiKey,
		apiKeyID: apiKeyID,
	}, nil
}

// Close closes any cached gRPC connection to Viam App.
func (u *directUploader) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn != nil {
		_ = u.conn.Close()
		u.conn = nil
		u.client = nil
	}
}

func (u *directUploader) ensureClient(ctx context.Context) (syncpb.DataSyncServiceClient, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.client != nil {
		return u.client, nil
	}

	baseURL := u.baseURL
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("direct upload base_url must start with http:// or https://, got %q", baseURL)
	}
	if !strings.HasSuffix(baseURL, ":443") {
		baseURL += ":443"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	creds := rpc.Credentials{
		Type:    rpc.CredentialsTypeAPIKey,
		Payload: u.apiKey,
	}
	dopt := rpc.WithEntityCredentials(u.apiKeyID, creds)
	conn, err := rpc.DialDirectGRPC(ctx, parsed.Host, u.logger, dopt)
	if err != nil {
		return nil, err
	}

	u.conn = conn
	u.client = syncpb.NewDataSyncServiceClient(conn)
	return u.client, nil
}

// uploadFile performs a streaming DataSync FileUpload from disk with tags/datasetIDs applied in UploadMetadata.
func (u *directUploader) uploadFile(
	ctx context.Context,
	filePath string,
	tags []string,
	datasetIDs []string,
) (string, error) {
	client, err := u.ensureClient(ctx)
	if err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}

	//nolint:gosec // filePath originates from internal staging dir.
	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	stream, err := client.FileUpload(ctx)
	if err != nil {
		return "", err
	}

	ext := filepath.Ext(absPath)
	metaReq := &syncpb.FileUploadRequest{
		UploadPacket: &syncpb.FileUploadRequest_Metadata{
			Metadata: &syncpb.UploadMetadata{
				PartId:        u.partID,
				Type:          syncpb.DataType_DATA_TYPE_FILE,
				FileName:      absPath,
				FileExtension: ext,
				Tags:          tags,
				DatasetIds:    datasetIDs,
			},
		},
	}
	if err := stream.Send(metaReq); err != nil {
		return "", err
	}

	buf := make([]byte, directUploadChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			dataReq := &syncpb.FileUploadRequest{
				UploadPacket: &syncpb.FileUploadRequest_FileContents{
					FileContents: &syncpb.FileData{Data: buf[:n]},
				},
			}
			if err := stream.Send(dataReq); err != nil {
				return "", err
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return "", rerr
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return "", err
	}
	return resp.GetBinaryDataId(), nil
}

// maybeEnqueueDirectUpload schedules an asynchronous upload job for the given file path if direct upload is enabled.
func (vs *videostore) maybeEnqueueDirectUpload(filePath string, r *SaveRequest) {
	if vs.directUploader == nil || vs.config.DirectUpload == nil || !vs.config.DirectUpload.Enabled {
		return
	}

	// Copy slices to avoid surprises if caller mutates.
	reqTags := append([]string(nil), r.Tags...)
	reqDatasets := append([]string(nil), r.DatasetIDs...)
	metadata := r.Metadata

	vs.workers.Add(func(ctx context.Context) {
		tags := buildDirectUploadTags(vs.config.DirectUpload.DefaultTags, reqTags, metadata)
		datasets := dedupeStrings(append(append([]string(nil), vs.config.DirectUpload.DatasetIDs...), reqDatasets...))

		binaryDataID, err := uploadWithRetry(
			ctx,
			vs.directUploader,
			filePath,
			tags,
			datasets,
			vs.config.DirectUpload.MaxRetries,
			time.Duration(vs.config.DirectUpload.InitialRetryDelayMillis)*time.Millisecond,
		)
		if err != nil {
			vs.logger.Errorw("direct upload failed", "file", filePath, "error", err)
			return
		}

		vs.logger.Debugw("direct upload succeeded", "file", filePath, "binary_data_id", binaryDataID)
		if vs.config.DirectUpload.DeleteAfterUpload == nil || *vs.config.DirectUpload.DeleteAfterUpload {
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				vs.logger.Warnw("failed deleting staged file after upload", "file", filePath, "error", err)
			}
		}
	})
}

func uploadWithRetry(
	ctx context.Context,
	u *directUploader,
	filePath string,
	tags []string,
	datasetIDs []string,
	maxRetries int,
	initialDelay time.Duration,
) (string, error) {
	var lastErr error
	delay := initialDelay
	attempts := maxRetries + 1 // include initial attempt
	for i := range attempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		binaryDataID, err := u.uploadFile(ctx, filePath, tags, datasetIDs)
		if err == nil {
			return binaryDataID, nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		if delay <= 0 {
			delay = time.Second
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
		delay *= 2
	}
	return "", lastErr
}

func buildDirectUploadTags(defaultTags, requestTags []string, metadata string) []string {
	tags := append([]string(nil), defaultTags...)
	tags = append(tags, requestTags...)

	if metadata != "" {
		// Make metadata searchable without requiring client changes.
		// Keep it stable + safe as a tag by sanitizing.
		meta := sanitizeTagComponent(metadata)
		if meta != "" {
			tag := "videostore-metadata-" + meta
			if len(tag) > maxMetadataTagLength {
				tag = tag[:maxMetadataTagLength]
			}
			tags = append(tags, tag)
		}
	}

	return dedupeStrings(tags)
}

func sanitizeTagComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
