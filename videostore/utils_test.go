package videostore

import (
	"testing"

	"go.viam.com/test"
)

const artifactStoragePath = "../.artifact/data/"

func TestGetVideoDuration(t *testing.T) {
	t.Run("Valid video file duration succeeds", func(t *testing.T) {
		duration, err := getVideoDuration(artifactStoragePath + "2024-09-06_15-00-03.mp4")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, duration.Seconds(), test.ShouldBeGreaterThan, 0.0)
	})
	t.Run("No duration video file errors", func(t *testing.T) {
		duration, err := getVideoDuration(artifactStoragePath + "zero_duration_video.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
	})
	t.Run("Not a video file errors", func(t *testing.T) {
		duration, err := getVideoDuration(artifactStoragePath + "not_video_file.txt")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
	})
	t.Run("Broken video file errors", func(t *testing.T) {
		duration, err := getVideoDuration(artifactStoragePath + "no_moov.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
	})
	t.Run("Nonexistent file errors", func(t *testing.T) {
		duration, err := getVideoDuration(artifactStoragePath + "nonexistent.mp4")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, duration.Seconds(), test.ShouldEqual, 0.0)
	})
}
