package videostore

import (
	"testing"

	"go.viam.com/test"
)

const artifactStoragePath = "../.artifact/data/"

func TestGetVideoDuration(t *testing.T) {
	t.Run("Valid video file succeeds", func(t *testing.T) {
		duration, width, height, codec, err := getVideoInfo(artifactStoragePath + "2024-09-06_15-00-03.mp4")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, duration.Seconds(), test.ShouldEqual, 30.0)
		test.That(t, width, test.ShouldEqual, 640)
		test.That(t, height, test.ShouldEqual, 480)
		test.That(t, codec, test.ShouldEqual, "h264")
	})
	t.Run("No duration video file errors", func(t *testing.T) {
		duration, width, height, codec, err := getVideoInfo(artifactStoragePath + "zero_duration_video.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "failed to get video info")
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, width, test.ShouldEqual, 0)
		test.That(t, height, test.ShouldEqual, 0)
		test.That(t, codec, test.ShouldEqual, "")
	})
	t.Run("Not a video file errors", func(t *testing.T) {
		duration, width, height, codec, err := getVideoInfo(artifactStoragePath + "not_video_file.txt")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "Invalid data found when processing input")
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, width, test.ShouldEqual, 0)
		test.That(t, height, test.ShouldEqual, 0)
		test.That(t, codec, test.ShouldEqual, "")
	})
	t.Run("Broken video file errors", func(t *testing.T) {
		duration, width, height, codec, err := getVideoInfo(artifactStoragePath + "no_moov.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "Invalid data found when processing input")
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, width, test.ShouldEqual, 0)
		test.That(t, height, test.ShouldEqual, 0)
		test.That(t, codec, test.ShouldEqual, "")
	})
	t.Run("Nonexistent file errors", func(t *testing.T) {
		duration, width, height, codec, err := getVideoInfo(artifactStoragePath + "nonexistent.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "No such file or directory")
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
		test.That(t, width, test.ShouldEqual, 0)
		test.That(t, height, test.ShouldEqual, 0)
		test.That(t, codec, test.ShouldEqual, "")
	})
}
