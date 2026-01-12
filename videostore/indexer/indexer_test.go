package indexer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/viam-modules/video-store/videostore/indexer"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

// artifactVideoFile is the reference video file we'll copy and rename to create test ranges.
const artifactVideoFile = "../../.artifact/data/2024-09-06_14-59-33.mp4"

// artifactDetails holds the video properties of the test artifact video file.
type artifactDetails struct {
	sizeBytes  int64
	duration   time.Duration
	durationMs int64
}

// getArtifactDetails returns the size and duration of the test artifact file.
func getArtifactDetails(t *testing.T) artifactDetails {
	t.Helper()
	assertArtifactExists(t)

	statInfo, err := os.Stat(artifactVideoFile)
	test.That(t, err, test.ShouldBeNil)

	videoInfo, err := vsutils.GetVideoInfo(artifactVideoFile)
	test.That(t, err, test.ShouldBeNil)

	return artifactDetails{
		sizeBytes:  statInfo.Size(),
		duration:   videoInfo.Duration,
		durationMs: videoInfo.Duration.Milliseconds(),
	}
}

// assertArtifactExists fails the test with a clear message if the artifact video file is missing.
func assertArtifactExists(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(artifactVideoFile); os.IsNotExist(err) {
		t.Fatalf("Test configuration error: artifact video file not found at %s. "+
			"Please ensure .artifact/data/ contains the required test files.", artifactVideoFile)
	}
}

// setupTestFilesOnDisk copies and renames video files to create specific time ranges.
func setupTestFilesOnDisk(t *testing.T, storagePath string, testFiles []testFileSpec) {
	t.Helper()

	for _, fileSpec := range testFiles {
		filename := fmt.Sprintf("%d.mp4", fileSpec.startTime.Unix())
		destPath := filepath.Join(storagePath, filename)

		data, err := os.ReadFile(artifactVideoFile)
		test.That(t, err, test.ShouldBeNil)
		err = os.WriteFile(destPath, data, 0o644)
		test.That(t, err, test.ShouldBeNil)
	}
}

// startTestIndexer creates, starts, and provides a cleanup function for an Indexer.
func startTestIndexer(t *testing.T, storagePath string, storageMaxGB int) (*indexer.Indexer, func()) {
	t.Helper()
	idx := indexer.NewIndexer(storagePath, storageMaxGB, 30, logging.NewTestLogger(t))
	err := idx.Start(context.Background())
	test.That(t, err, test.ShouldBeNil)

	cleanup := func() {
		errClose := idx.Close()
		test.That(t, errClose, test.ShouldBeNil)
	}
	return idx, cleanup
}

// waitForIndexerToProcess waits for the indexer to process the expected number of files.
func waitForIndexerToProcess(t *testing.T, idx *indexer.Indexer, expectedFileCount int) indexer.VideoRanges {
	t.Helper()

	timeout := time.After(15 * time.Second)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for indexer to process %d files", expectedFileCount)
		case <-tick:
			videoRanges, err := idx.GetVideoList(context.Background())
			if err != nil {
				t.Logf("GetVideoList error (continuing): %v", err)
				continue
			}

			t.Logf("indexer processed %d files, expecting %d", videoRanges.VideoCount, expectedFileCount)
			if videoRanges.VideoCount == expectedFileCount {
				return videoRanges
			}
		}
	}
}

// testFileSpec specifies a test file's timestamp.
type testFileSpec struct {
	startTime time.Time
}

func TestGetVideoList(t *testing.T) {
	details := getArtifactDetails(t)
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Times for the "multiple files mixed contiguity" test case.
	multiFile1Start := baseTime
	multiFile2Start := multiFile1Start.Add(details.duration)                       // contiguous
	multiFile3Start := multiFile2Start.Add(details.duration).Add(10 * time.Second) // large gap
	multiFile4Start := multiFile3Start.Add(details.duration).Add(2 * time.Second)  // small gap
	multiFile5Start := multiFile4Start.Add(details.duration).Add(20 * time.Second) // large gap

	testCases := []struct {
		name                 string
		testFiles            []testFileSpec
		expectedRanges       []indexer.VideoRange
		expectedDurationMs   int64
		expectedStorageBytes int64
	}{
		{
			name:                 "no files",
			testFiles:            []testFileSpec{},
			expectedRanges:       []indexer.VideoRange{},
			expectedDurationMs:   0,
			expectedStorageBytes: 0,
		},
		{
			name: "single file",
			testFiles: []testFileSpec{
				{startTime: baseTime},
			},
			expectedRanges: []indexer.VideoRange{
				{Start: baseTime, End: baseTime.Add(details.duration)},
			},
			expectedDurationMs:   details.durationMs,
			expectedStorageBytes: details.sizeBytes,
		},
		{
			name: "two contiguous files",
			testFiles: []testFileSpec{
				{startTime: baseTime},
				{startTime: baseTime.Add(details.duration)},
			},
			expectedRanges: []indexer.VideoRange{
				{Start: baseTime, End: baseTime.Add(2 * details.duration)}, // merged into one range
			},
			expectedDurationMs:   2 * details.durationMs,
			expectedStorageBytes: 2 * details.sizeBytes,
		},
		{
			name: "two files with small gap (within slop)",
			testFiles: []testFileSpec{
				{startTime: baseTime},
				{startTime: baseTime.Add(details.duration).Add(2 * time.Second)},
			},
			expectedRanges: []indexer.VideoRange{
				{Start: baseTime, End: baseTime.Add(2 * details.duration).Add(2 * time.Second)}, // merged despite gap
			},
			expectedDurationMs:   2 * details.durationMs,
			expectedStorageBytes: 2 * details.sizeBytes,
		},
		{
			name: "two files with large gap (exceeds slop)",
			testFiles: []testFileSpec{
				{startTime: baseTime},
				{startTime: baseTime.Add(details.duration).Add(10 * time.Second)},
			},
			expectedRanges: []indexer.VideoRange{
				{Start: baseTime, End: baseTime.Add(details.duration)},
				{Start: baseTime.Add(details.duration).Add(10 * time.Second), End: baseTime.Add(2 * details.duration).Add(10 * time.Second)},
			},
			expectedDurationMs:   2 * details.durationMs,
			expectedStorageBytes: 2 * details.sizeBytes,
		},
		{
			name: "multiple files mixed contiguity",
			testFiles: []testFileSpec{
				{startTime: multiFile1Start},
				{startTime: multiFile2Start},
				{startTime: multiFile3Start},
				{startTime: multiFile4Start},
				{startTime: multiFile5Start},
			},
			expectedRanges: []indexer.VideoRange{
				{Start: multiFile1Start, End: multiFile2Start.Add(details.duration)}, // file1 & file2 merged
				{Start: multiFile3Start, End: multiFile4Start.Add(details.duration)}, // file3 & file4 merged
				{Start: multiFile5Start, End: multiFile5Start.Add(details.duration)}, // file5 separate
			},
			expectedDurationMs:   5 * details.durationMs,
			expectedStorageBytes: 5 * details.sizeBytes,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assertArtifactExists(t)

			storagePath := t.TempDir()
			setupTestFilesOnDisk(t, storagePath, tc.testFiles)

			idx, cleanup := startTestIndexer(t, storagePath, 100)
			defer cleanup()

			videoRanges := waitForIndexerToProcess(t, idx, len(tc.testFiles))

			// Verify the number of ranges matches
			test.That(t, len(videoRanges.Ranges), test.ShouldEqual, len(tc.expectedRanges))

			// Verify each range's start and end times
			for i, expectedRange := range tc.expectedRanges {
				actualRange := videoRanges.Ranges[i]
				test.That(t, actualRange.Start.Unix(), test.ShouldEqual, expectedRange.Start.Unix())
				test.That(t, actualRange.End.Unix(), test.ShouldEqual, expectedRange.End.Unix())

				t.Logf("Range %d: %s to %s (duration: %v)",
					i+1,
					actualRange.Start.Format("15:04:05"),
					actualRange.End.Format("15:04:05"),
					actualRange.End.Sub(actualRange.Start))
			}

			test.That(t, videoRanges.VideoCount, test.ShouldEqual, len(tc.testFiles))

			test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, tc.expectedDurationMs)
			test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, tc.expectedStorageBytes)
		})
	}
}

func TestIndexerWithMalformedFiles(t *testing.T) {
	storagePath := t.TempDir()

	invalidFiles := []string{
		"invalid_name.mp4", // can't extract timestamp
		"not_a_video.txt",  // wrong extension
		"1234567890",       // no extension
	}

	for _, filename := range invalidFiles {
		filePath := filepath.Join(storagePath, filename)
		err := os.WriteFile(filePath, []byte("dummy content"), 0o644)
		test.That(t, err, test.ShouldBeNil)
	}

	validFilename := fmt.Sprintf("%d.mp4", time.Now().Unix())
	validPath := filepath.Join(storagePath, validFilename)

	data, err := os.ReadFile(artifactVideoFile)
	test.That(t, err, test.ShouldBeNil)
	err = os.WriteFile(validPath, data, 0o644)
	test.That(t, err, test.ShouldBeNil)
	expectedValidFiles := 1
	details := getArtifactDetails(t)

	idx, cleanup := startTestIndexer(t, storagePath, 100)
	defer cleanup()

	videoRanges := waitForIndexerToProcess(t, idx, expectedValidFiles)

	test.That(t, videoRanges.VideoCount, test.ShouldEqual, expectedValidFiles)
	test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, details.durationMs)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, details.sizeBytes)
}

func TestManuallyDeletedFile(t *testing.T) {
	details := getArtifactDetails(t)

	storagePath := t.TempDir()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	filename := fmt.Sprintf("%d.mp4", baseTime.Unix()) // to be deleted
	filePath := filepath.Join(storagePath, filename)

	data, err := os.ReadFile(artifactVideoFile)
	test.That(t, err, test.ShouldBeNil)
	err = os.WriteFile(filePath, data, 0o644)
	test.That(t, err, test.ShouldBeNil)

	idx, cleanup := startTestIndexer(t, storagePath, 100)
	defer cleanup()

	videoRanges := waitForIndexerToProcess(t, idx, 1)
	test.That(t, videoRanges.VideoCount, test.ShouldEqual, 1)
	test.That(t, len(videoRanges.Ranges), test.ShouldEqual, 1)
	test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, details.durationMs)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, details.sizeBytes)
	t.Logf("File indexed successfully: %s", filename)

	// Manually delete the file from disk
	err = os.Remove(filePath)
	test.That(t, err, test.ShouldBeNil)
	t.Logf("File manually deleted from disk: %s", filePath)

	// Wait for indexer to detect the missing file and remove it from index
	videoRanges = waitForIndexerToProcess(t, idx, 0)
	test.That(t, videoRanges.VideoCount, test.ShouldEqual, 0)
	test.That(t, len(videoRanges.Ranges), test.ShouldEqual, 0)
	test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, 0)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, 0)
	t.Logf("Indexer successfully detected and removed manually deleted file")
}

func TestManuallyDeletedDB(t *testing.T) {
	details := getArtifactDetails(t)

	storagePath := t.TempDir()
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	testFiles := []testFileSpec{
		{startTime: baseTime},
	}
	setupTestFilesOnDisk(t, storagePath, testFiles)

	idx, cleanup := startTestIndexer(t, storagePath, 100)
	defer cleanup()

	videoRanges := waitForIndexerToProcess(t, idx, 1)
	test.That(t, videoRanges.VideoCount, test.ShouldEqual, 1)
	test.That(t, len(videoRanges.Ranges), test.ShouldEqual, 1)
	test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, details.durationMs)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, details.sizeBytes)

	// Manually delete the .db file
	err := os.Remove(filepath.Join(storagePath, "index.sqlite.db"))
	test.That(t, err, test.ShouldBeNil)

	// Wait for the indexer to recover
	timeout := time.After(10 * time.Second)
	tick := time.Tick(100 * time.Millisecond)
	recoveryDetected := false
	for !recoveryDetected {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for indexer to recover from database deletion")
		case <-tick:
			ranges, err := idx.GetVideoList(context.Background())
			if err == nil && ranges.VideoCount == 1 {
				recoveryDetected = true
			}
		}
	}

	// Create a second file after recovery
	secondFileSpec := testFileSpec{startTime: baseTime.Add(details.duration)}
	secondFileName := fmt.Sprintf("%d.mp4", secondFileSpec.startTime.Unix())
	secondFilePath := filepath.Join(storagePath, secondFileName)

	data, err := os.ReadFile(artifactVideoFile)
	test.That(t, err, test.ShouldBeNil)
	err = os.WriteFile(secondFilePath, data, 0o644)
	test.That(t, err, test.ShouldBeNil)

	// Wait for indexer to detect and index the new file
	videoRanges = waitForIndexerToProcess(t, idx, 2)
	test.That(t, videoRanges.VideoCount, test.ShouldEqual, 2)
	test.That(t, videoRanges.TotalDurationMs, test.ShouldEqual, 2*details.durationMs)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldEqual, 2*details.sizeBytes)
	test.That(t, len(videoRanges.Ranges), test.ShouldEqual, 1) // should be contiguous
}

func TestDeletions(t *testing.T) {
	storagePath := t.TempDir()
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	details := getArtifactDetails(t)

	// Define storage limit and calculate how many files fit JUUUST under it. Insert enough files to be just under the limit.
	const storageMaxGB = 1
	maxStorageBytes := int64(storageMaxGB * vsutils.Gigabyte)
	initialFileCount := int(maxStorageBytes / details.sizeBytes)
	t.Logf("Artifact size: %d bytes. Max storage: %d bytes. Calculated initial file count: %d",
		details.sizeBytes, maxStorageBytes, initialFileCount)
	initialTestFiles := make([]testFileSpec, initialFileCount)
	for i := range initialFileCount {
		initialTestFiles[i] = testFileSpec{
			startTime: baseTime.Add(time.Duration(i) * details.duration),
		}
	}
	setupTestFilesOnDisk(t, storagePath, initialTestFiles)

	// After setting up the initial files, start the indexer and wait for it to process them
	idx, cleanup := startTestIndexer(t, storagePath, storageMaxGB)
	defer cleanup()
	t.Logf("Waiting for indexer to process initial %d files...", initialFileCount)
	videoRanges := waitForIndexerToProcess(t, idx, initialFileCount)
	test.That(t, videoRanges.VideoCount, test.ShouldEqual, initialFileCount)
	test.That(t, videoRanges.StorageUsedBytes, test.ShouldBeLessThanOrEqualTo, maxStorageBytes)
	t.Logf("Successfully indexed %d files, total size %.3f GB (under limit)",
		videoRanges.VideoCount, float64(videoRanges.StorageUsedBytes)/vsutils.Gigabyte)

	// Add more files to exceed the storage limit
	const numFilesToAdd = 10
	t.Logf("Adding %d more files to trigger cleanup...", numFilesToAdd)
	newTestFiles := make([]testFileSpec, numFilesToAdd)
	for i := range numFilesToAdd {
		newTestFiles[i] = testFileSpec{
			startTime: baseTime.Add(time.Duration(initialFileCount+i) * details.duration),
		}
	}
	setupTestFilesOnDisk(t, storagePath, newTestFiles)

	// Wait for the indexer to process the new files and perform cleanup.
	// The oldest files should be deleted, bringing the count back to initialFileCount,
	// and shifting the start time of the video range forward.
	t.Logf("Waiting for indexer to perform cleanup, expecting file count to return to %d...", initialFileCount)
	expectedNewStartTime := baseTime.Add(time.Duration(numFilesToAdd) * details.duration)

	timeout := time.After(15 * time.Second)
	tick := time.Tick(200 * time.Millisecond)
	var finalVideoRanges indexer.VideoRanges
	cleanupComplete := false

	for !cleanupComplete {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for cleanup")
		case <-tick:
			videoRanges, err := idx.GetVideoList(context.Background())
			if err != nil {
				t.Logf("GetVideoList error (continuing): %v", err)
				continue
			}

			if len(videoRanges.Ranges) == 0 {
				t.Logf("no ranges found yet, continuing")
				continue
			}

			currentStartTime := videoRanges.Ranges[0].Start
			t.Logf("waiting for cleanup; current count: %d (want %d), start: %v (want %v)",
				videoRanges.VideoCount, initialFileCount,
				currentStartTime.Format("15:04:05"), expectedNewStartTime.Format("15:04:05"))

			if videoRanges.VideoCount == initialFileCount && currentStartTime.Unix() == expectedNewStartTime.Unix() {
				t.Log("cleanup complete")
				finalVideoRanges = videoRanges
				cleanupComplete = true
			}
		}
	}

	expectedFinalStorageBytes := int64(initialFileCount) * details.sizeBytes
	expectedFinalDurationMs := int64(initialFileCount) * details.durationMs

	test.That(t, finalVideoRanges.VideoCount, test.ShouldEqual, initialFileCount)
	test.That(t, finalVideoRanges.StorageUsedBytes, test.ShouldEqual, expectedFinalStorageBytes)
	test.That(t, finalVideoRanges.TotalDurationMs, test.ShouldEqual, expectedFinalDurationMs)
	test.That(t, finalVideoRanges.Ranges[0].Start.Unix(), test.ShouldEqual, expectedNewStartTime.Unix())

	t.Logf("Cleanup successful. Final file count: %d, Final storage: %.3f GB",
		finalVideoRanges.VideoCount, float64(finalVideoRanges.StorageUsedBytes)/vsutils.Gigabyte)
}

func TestUnreadableFileCleanup(t *testing.T) {
	storagePath := t.TempDir()
	segmentDur := 1
	idx := indexer.NewIndexer(storagePath, 1, segmentDur, logging.NewTestLogger(t))
	err := idx.Start(context.Background())
	test.That(t, err, test.ShouldBeNil)
	defer idx.Close()

	// Create an "unreadable" file (just a dummy text file with .mp4 extension)
	unreadableFileName := fmt.Sprintf("%d.mp4", time.Now().Unix())
	unreadablePath := filepath.Join(storagePath, unreadableFileName)
	err = os.WriteFile(unreadablePath, []byte("garbage data"), 0o644)
	test.That(t, err, test.ShouldBeNil)

	// 1. Initially it should be skipped because it's within the grace period
	// Wait a bit to let the indexer tick
	time.Sleep(2 * time.Second)
	_, err = os.Stat(unreadablePath)
	test.That(t, err, test.ShouldBeNil) // Should still exist

	// 2. Rename the file to be 1 hour in the past
	oldTime := time.Now().Add(-1 * time.Hour)
	oldFileName := fmt.Sprintf("%d.mp4", oldTime.Unix())
	oldPath := filepath.Join(storagePath, oldFileName)
	err = os.Rename(unreadablePath, oldPath)
	test.That(t, err, test.ShouldBeNil)

	// 3. Wait for the indexer to tick and delete the file
	timeout := time.After(5 * time.Second)
	tick := time.Tick(500 * time.Millisecond)
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for unreadable file to be deleted")
		case <-tick:
			if _, err := os.Stat(oldPath); os.IsNotExist(err) {
				return // Success
			}
		}
	}
}
