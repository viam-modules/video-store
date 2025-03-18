package videostore

import (
	"testing"
	"time"

	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

const artifactStoragePath = "../.artifact/data/"

func TestGetVideoInfo(t *testing.T) {
	t.Run("Valid video file succeeds", func(t *testing.T) {
		info, err := getVideoInfo(artifactStoragePath + "2024-09-06_15-00-03.mp4")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, info.duration.Seconds(), test.ShouldEqual, 30.0)
		test.That(t, info.width, test.ShouldEqual, 640)
		test.That(t, info.height, test.ShouldEqual, 480)
		test.That(t, info.codec, test.ShouldEqual, "h264")
	})
	t.Run("No duration video file errors", func(t *testing.T) {
		info, err := getVideoInfo(artifactStoragePath + "zero_duration_video.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "get_video_info failed for file")
		test.That(t, info.duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, info.width, test.ShouldEqual, 0)
		test.That(t, info.height, test.ShouldEqual, 0)
		test.That(t, info.codec, test.ShouldEqual, "")
	})
	t.Run("Not a video file errors", func(t *testing.T) {
		info, err := getVideoInfo(artifactStoragePath + "not_video_file.txt")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "Invalid data found when processing input")
		test.That(t, info.duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, info.width, test.ShouldEqual, 0)
		test.That(t, info.height, test.ShouldEqual, 0)
		test.That(t, info.codec, test.ShouldEqual, "")
	})
	t.Run("Broken video file errors", func(t *testing.T) {
		info, err := getVideoInfo(artifactStoragePath + "no_moov.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "Invalid data found when processing input")
		test.That(t, info.duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, info.width, test.ShouldEqual, 0)
		test.That(t, info.height, test.ShouldEqual, 0)
		test.That(t, info.codec, test.ShouldEqual, "")
	})
	t.Run("Nonexistent file errors", func(t *testing.T) {
		info, err := getVideoInfo(artifactStoragePath + "nonexistent.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "No such file or directory")
		test.That(t, info.duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, info.width, test.ShouldEqual, 0)
		test.That(t, info.height, test.ShouldEqual, 0)
		test.That(t, info.codec, test.ShouldEqual, "")
	})
}

func TestMatchStorageToRange(t *testing.T) {
	logger := logging.NewTestLogger(t)
	t.Run("Match request within one segment", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-05_16-36-20.mp4",
			artifactStoragePath + "2025-03-05_16-36-59.mp4",
			artifactStoragePath + "2025-03-05_16-37-38.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-05_16-36-30")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-05_16-36-40")
		test.That(t, err, test.ShouldBeNil)
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-05_16-36-20.mp4", inpoint: float64Ptr(10.00), outpoint: float64Ptr(20.00)},
		}
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request spanning multiple segments", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2024-09-06_15-00-03.mp4",
			artifactStoragePath + "2024-09-06_15-00-33.mp4",
			artifactStoragePath + "2024-09-06_15-01-03.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2024-09-06_15-00-10")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2024-09-06_15-01-00")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2024-09-06_15-00-03.mp4", inpoint: float64Ptr(7.00)},
			{filePath: artifactStoragePath + "2024-09-06_15-00-33.mp4", outpoint: float64Ptr(27.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request along segment boundary", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2024-09-06_15-00-03.mp4",
			artifactStoragePath + "2024-09-06_15-00-33.mp4",
			artifactStoragePath + "2024-09-06_15-01-03.mp4",
			artifactStoragePath + "2024-09-06_15-01-33.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2024-09-06_15-00-33")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2024-09-06_15-01-33")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2024-09-06_15-00-33.mp4"},
			{filePath: artifactStoragePath + "2024-09-06_15-01-03.mp4"},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request within gap in data", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-05_16-36-20.mp4",
			artifactStoragePath + "2025-03-05_16-36-59.mp4",
			artifactStoragePath + "2025-03-05_16-37-38.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-05_16-36-53")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-05_16-36-55")
		test.That(t, err, test.ShouldBeNil)
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldBeEmpty)
	})

	t.Run("Match request spanning gap in segment data", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-05_16-36-20.mp4",
			artifactStoragePath + "2025-03-05_16-36-59.mp4",
			artifactStoragePath + "2025-03-05_16-37-38.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-05_16-36-40")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-05_16-37-10")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-05_16-36-20.mp4", inpoint: float64Ptr(20.00)},
			{filePath: artifactStoragePath + "2025-03-05_16-36-59.mp4", outpoint: float64Ptr(11.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request start range in data gap", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-05_16-36-20.mp4",
			artifactStoragePath + "2025-03-05_16-36-59.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-05_16-36-54")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-05_16-37-20")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-05_16-36-59.mp4", outpoint: float64Ptr(21.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request end range in data gap", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-05_16-36-20.mp4",
			artifactStoragePath + "2025-03-05_16-36-59.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-05_16-36-30")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-05_16-36-55")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-05_16-36-20.mp4", inpoint: float64Ptr(10.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request spanning size change boundary", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-11_11-49-37.mp4",
			artifactStoragePath + "2025-03-11_11-49-58.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-11_11-49-47")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-11_11-50-47")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-11_11-49-37.mp4", inpoint: float64Ptr(10.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})

	t.Run("Match request spanning codec change boundary", func(t *testing.T) {
		fileList := []string{
			artifactStoragePath + "2025-03-11_16-14-40.mp4",
			artifactStoragePath + "2025-03-11_16-15-10.mp4",
		}
		fileWithDateList := createAndSortFileWithDateList(fileList)
		startTime, err := ParseDateTimeString("2025-03-11_16-14-50")
		test.That(t, err, test.ShouldBeNil)
		endTime, err := ParseDateTimeString("2025-03-11_16-15-30")
		test.That(t, err, test.ShouldBeNil)
		expected := []concatFileEntry{
			{filePath: artifactStoragePath + "2025-03-11_16-14-40.mp4", inpoint: float64Ptr(10.00)},
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldResemble, expected)
	})
}

// float64Ptr returns a pointer to the given float64 value.
func float64Ptr(f float64) *float64 {
	return &f
}

func TestExtractDateTimeFromFilename(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		expectedTime   time.Time
		expectedErrMsg string
	}{
		{
			name:         "Unix timestamp format",
			filename:     "camera_1694019633.mp4",
			expectedTime: time.Unix(1694019633, 0).UTC(),
		},
		{
			name:     "Legacy format (local time)",
			filename: "camera_2024-09-06_15-00-33.mp4",
			// 15:00:33 EDT (UTC-4) is equivalent to 19:00:33 UTC
			expectedTime: time.Date(2024, 9, 6, 19, 0, 33, 0, time.UTC),
		},
		{
			name:           "Invalid format",
			filename:       "camera_invalid.mp4",
			expectedTime:   time.Time{},
			expectedErrMsg: "invalid file name: camera_invalid.mp4",
		},
		{
			name:           "Missing timezone",
			filename:       "camera_2024-09-06_15-00-33.mp4",
			expectedTime:   time.Time{},
			expectedErrMsg: "timezone must be provided for parsing legacy format timestamps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractDateTimeFromFilename(tt.filename)
			if tt.expectedErrMsg != "" {
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldEqual, tt.expectedErrMsg)
				return
			}
			test.That(t, err, test.ShouldBeNil)
			test.That(t, got.Equal(tt.expectedTime), test.ShouldBeTrue)
			test.That(t, got.Location(), test.ShouldEqual, time.UTC)
		})
	}
}
