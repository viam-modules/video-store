package videostore

import (
	"testing"

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
		expected := []string{
			"file '../.artifact/data/2025-03-05_16-36-20.mp4'",
			"inpoint 10.00",
			"outpoint 20.00",
		}
		test.That(t, matchedFiles, test.ShouldHaveLength, 3)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2024-09-06_15-00-03.mp4'",
			"inpoint 7.00",
			"file '../.artifact/data/2024-09-06_15-00-33.mp4'",
			"outpoint 27.00",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 4)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2024-09-06_15-00-33.mp4'",
			"file '../.artifact/data/2024-09-06_15-01-03.mp4'",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 2)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		expected := []string{
			"file '../.artifact/data/2025-03-05_16-36-20.mp4'",
			"inpoint 20.00",
			"file '../.artifact/data/2025-03-05_16-36-59.mp4'",
			"outpoint 11.00",
		}
		test.That(t, matchedFiles, test.ShouldHaveLength, 4)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2025-03-05_16-36-59.mp4'",
			"outpoint 21.00",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 2)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2025-03-05_16-36-20.mp4'",
			"inpoint 10.00",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 2)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2025-03-11_11-49-37.mp4'",
			"inpoint 10.00",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 2)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
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
		expected := []string{
			"file '../.artifact/data/2025-03-11_16-14-40.mp4'",
			"inpoint 10.00",
		}
		matchedFiles := matchStorageToRange(fileWithDateList, startTime, endTime, logger)
		test.That(t, matchedFiles, test.ShouldHaveLength, 2)
		for i, exp := range expected {
			test.That(t, matchedFiles[i], test.ShouldEqual, exp)
		}
	})
}
