package videostore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/services/video"
	"go.viam.com/test"
)

const (
	artifactStoragePath = "../.artifact/data"
)

// getArtifactStoragePath returns the absolute path to the artifact storage directory.
func getArtifactStoragePath(t *testing.T) string {
	t.Helper()
	currentDir, err := os.Getwd()
	test.That(t, err, test.ShouldBeNil)
	storagePath := filepath.Join(currentDir, artifactStoragePath)
	return storagePath
}

// createTestVideoStore creates a read-only videostore for testing FetchStream.
func createTestVideoStore(t *testing.T, storagePath string) VideoStore {
	t.Helper()
	logger := logging.NewTestLogger(t)

	uploadPath := t.TempDir()

	config := Config{
		Name: "test-video-store",
		Type: SourceTypeReadOnly,
		Storage: StorageConfig{
			StoragePath:          storagePath,
			UploadPath:           uploadPath,
			SizeGB:               10,
			OutputFileNamePrefix: "test-video-store",
		},
	}

	ctx := context.Background()
	vs, err := NewReadOnlyVideoStore(ctx, config, logger)
	test.That(t, err, test.ShouldBeNil)
	t.Cleanup(func() {
		vs.Close()
	})

	return vs
}

func TestFetchStreamValidRequest(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// Use a known time range from the artifact files.
	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	var chunks []video.Chunk
	var totalBytes int

	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		chunks = append(chunks, chunk)
		totalBytes += len(chunk.Data)
		return nil
	})

	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(chunks), test.ShouldBeGreaterThan, 0)
	test.That(t, totalBytes, test.ShouldBeGreaterThan, 0)

	// Verify all chunks have the expected container format
	for _, chunk := range chunks {
		test.That(t, chunk.Container, test.ShouldEqual, videoFormat)
		test.That(t, len(chunk.Data), test.ShouldBeGreaterThan, 0)
		test.That(t, len(chunk.Data), test.ShouldBeLessThanOrEqualTo, streamingChunkSize)
	}
}

func TestFetchStreamContextCancellation(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// Use a time range with valid video data that produces multiple chunks
	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 1, 33, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var chunksReceived int

	err := vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		chunksReceived++
		// Cancel after receiving a few chunks
		if chunksReceived >= 2 {
			cancel()
		}
		return nil
	})

	// Should return context.Canceled error
	test.That(t, errors.Is(err, context.Canceled), test.ShouldBeTrue)
	test.That(t, chunksReceived, test.ShouldBeGreaterThanOrEqualTo, 2)
}

func TestFetchStreamEmitError(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	expectedErr := errors.New("emit failed")
	var chunksReceived int

	err := vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		chunksReceived++
		if chunksReceived >= 2 {
			return expectedErr
		}
		return nil
	})

	test.That(t, errors.Is(err, expectedErr), test.ShouldBeTrue)
	test.That(t, chunksReceived, test.ShouldEqual, 2)
}

func TestFetchStreamInvalidTimeRange(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// 'from' is after 'to'
	from := time.Date(2024, 9, 6, 16, 0, 0, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 0, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	err := vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		t.Fatal("emit should not be called for invalid request")
		return nil
	})

	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "after")
}

func TestFetchStreamNoMatchingData(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// Time range with no video data
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2020, 1, 1, 0, 1, 0, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	err := vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		t.Fatal("emit should not be called when no matching data")
		return nil
	})

	test.That(t, err, test.ShouldNotBeNil)
}

func TestFetchStreamLocalTimeConversion(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// Use local time - should be converted to UTC internally
	loc, err := time.LoadLocation("America/New_York")
	test.That(t, err, test.ShouldBeNil)

	// 2024-09-06 11:00:33 EDT = 2024-09-06 15:00:33 UTC
	from := time.Date(2024, 9, 6, 11, 0, 33, 0, loc)
	to := time.Date(2024, 9, 6, 11, 0, 50, 0, loc)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	var chunks []video.Chunk

	err = vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})

	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(chunks), test.ShouldBeGreaterThan, 0)
}

func TestFetchStreamChunkSize(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	var chunks []video.Chunk

	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		chunks = append(chunks, chunk)
		return nil
	})

	test.That(t, err, test.ShouldBeNil)

	// All chunks except possibly the last should be exactly streamingChunkSize 64KB
	for i, chunk := range chunks {
		if i < len(chunks)-1 {
			test.That(t, len(chunk.Data), test.ShouldEqual, streamingChunkSize)
		} else {
			// Last chunk can be smaller
			test.That(t, len(chunk.Data), test.ShouldBeLessThanOrEqualTo, streamingChunkSize)
			test.That(t, len(chunk.Data), test.ShouldBeGreaterThan, 0)
		}
	}
}

func TestFetchStreamReassemblesVideo(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	var videoData []byte

	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		videoData = append(videoData, chunk.Data...)
		return nil
	})

	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(videoData), test.ShouldBeGreaterThan, 0)

	// Write reassembled video to temp file and verify it's valid
	tempFile := filepath.Join(t.TempDir(), "reassembled.mp4")
	err = os.WriteFile(tempFile, videoData, 0o644)
	test.That(t, err, test.ShouldBeNil)

	// Verify the reassembled file is a valid MP4 by checking the file header
	// MP4 files typically start with ftyp box
	// TODO(seanp): we can check headers against request container format
	file, err := os.Open(tempFile)
	test.That(t, err, test.ShouldBeNil)
	defer file.Close()

	header := make([]byte, 12)
	n, err := file.Read(header)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, n, test.ShouldEqual, 12)

	// Check for ftyp box (bytes 4-7 should be "ftyp")
	ftyp := string(header[4:8])
	test.That(t, ftyp, test.ShouldEqual, "ftyp")
}

func TestFetchStreamTemporaryFileCleanup(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()

	// Count temp files before
	tempDir := os.TempDir()
	entriesBefore, err := os.ReadDir(tempDir)
	test.That(t, err, test.ShouldBeNil)
	countBefore := len(entriesBefore)

	err = vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		return nil
	})

	test.That(t, err, test.ShouldBeNil)

	// Count temp files after - should be the same (cleanup happened)
	entriesAfter, err := os.ReadDir(tempDir)
	test.That(t, err, test.ShouldBeNil)
	countAfter := len(entriesAfter)

	// Allow for some variance due to other processes
	test.That(t, countAfter, test.ShouldBeLessThanOrEqualTo, countBefore+1)
}

func TestFetchStreamEmptyChunkHandling(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()

	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		// All chunks should have data - empty reads shouldn't be emitted
		test.That(t, len(chunk.Data), test.ShouldBeGreaterThan, 0)
		return nil
	})

	test.That(t, err, test.ShouldBeNil)
}

// TestFetchRequestValidate tests the FetchRequest.Validate method.
func TestFetchRequestValidate(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := &FetchRequest{
			From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC),
		}
		err := req.Validate()
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("from equals to", func(t *testing.T) {
		now := time.Now()
		req := &FetchRequest{
			From: now,
			To:   now,
		}
		err := req.Validate()
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("from after to", func(t *testing.T) {
		req := &FetchRequest{
			From: time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC),
			To:   time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC),
		}
		err := req.Validate()
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "after")
	})
}

// TestFetchStream_CompareWithFetch verifies that FetchStream produces the same
// video data as the non-streaming Fetch method.
func TestFetchStreamCompareWithFetch(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()

	// Get video via streaming
	var streamedData []byte
	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		streamedData = append(streamedData, chunk.Data...)
		return nil
	})
	test.That(t, err, test.ShouldBeNil)

	// Get video via non-streaming Fetch
	fetchResp, err := vs.Fetch(ctx, req)
	test.That(t, err, test.ShouldBeNil)

	// Both should produce the same data
	test.That(t, len(streamedData), test.ShouldEqual, len(fetchResp.Video))
	test.That(t, streamedData, test.ShouldResemble, fetchResp.Video)
}

// TestFetchStreamContextTimeout tests that FetchStream respects context timeout.
func TestFetchStreamContextTimeout(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	// Use a time range with valid video data
	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 1, 33, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	// Very short timeout - context will be cancelled before streaming starts
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout to trigger
	time.Sleep(10 * time.Millisecond)

	err := vs.FetchStream(ctx, req, func(_ video.Chunk) error {
		return nil
	})

	// Should get an error - either deadline exceeded, context canceled,
	// or the concat operation may have already happened before context check
	test.That(t, err, test.ShouldNotBeNil)
}

// TestFetchStreamEOFHandling tests that EOF is properly handled.
func TestFetchStreamEOFHandling(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	req := &FetchRequest{
		From: from,
		To:   to,
	}

	ctx := context.Background()
	var lastChunkSize int

	err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
		lastChunkSize = len(chunk.Data)
		return nil
	})

	// Should complete without error (EOF handled internally)
	test.That(t, err, test.ShouldBeNil)

	// Last chunk was emitted (non-zero)
	test.That(t, lastChunkSize, test.ShouldBeGreaterThan, 0)
}

// TestFetchStreamContainerFormat tests that the correct container format is returned in chunks.
func TestFetchStreamContainerFormat(t *testing.T) {
	storagePath := getArtifactStoragePath(t)
	vs := createTestVideoStore(t, storagePath)

	from := time.Date(2024, 9, 6, 15, 0, 33, 0, time.UTC)
	to := time.Date(2024, 9, 6, 15, 0, 50, 0, time.UTC)

	testCases := []struct {
		name              string
		container         ContainerFormat
		expectedContainer string
	}{
		{"default container", ContainerDefault, "mp4"},
		{"mp4 container", ContainerMP4, "mp4"},
		{"fmp4 container", ContainerFMP4, "fmp4"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &FetchRequest{
				From:      from,
				To:        to,
				Container: tc.container,
			}

			ctx := context.Background()
			var receivedContainers []string

			err := vs.FetchStream(ctx, req, func(chunk video.Chunk) error {
				receivedContainers = append(receivedContainers, chunk.Container)
				return nil
			})

			test.That(t, err, test.ShouldBeNil)
			test.That(t, len(receivedContainers), test.ShouldBeGreaterThan, 0)

			// All chunks should have the expected container format
			for _, container := range receivedContainers {
				test.That(t, container, test.ShouldEqual, tc.expectedContainer)
			}
		})
	}
}
