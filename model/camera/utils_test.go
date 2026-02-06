package camera

import (
	"testing"
	"time"

	"github.com/viam-modules/video-store/videostore"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/test"
)

func TestToSaveCommand(t *testing.T) {
	t.Run("valid command without tags", func(t *testing.T) {
		command := map[string]interface{}{
			"from":     "2024-09-06_15-00-33",
			"to":       "2024-09-06_15-01-33",
			"metadata": "test-metadata",
			"async":    true,
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, req.Metadata, test.ShouldEqual, "test-metadata")
		test.That(t, req.Async, test.ShouldBeTrue)
		test.That(t, req.Tags, test.ShouldBeNil)
	})

	t.Run("valid command with tags", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
			"tags": []interface{}{"location", "quality"},
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, req.Tags, test.ShouldResemble, []string{"location", "quality"})
	})

	t.Run("valid command with string slice tags", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
			"tags": []string{"location", "quality"},
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, req.Tags, test.ShouldResemble, []string{"location", "quality"})
	})

	t.Run("valid command with empty tags array", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
			"tags": []interface{}{},
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, len(req.Tags), test.ShouldEqual, 0)
	})

	t.Run("invalid tags - non-string element", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
			"tags": []interface{}{"location", 123, "quality"},
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "tag at index 1 is not a string")
		test.That(t, req, test.ShouldBeNil)
	})

	t.Run("invalid tags - not an array", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
			"tags": "location",
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "tags must be an array of strings")
		test.That(t, req, test.ShouldBeNil)
	})

	t.Run("missing from timestamp", func(t *testing.T) {
		command := map[string]interface{}{
			"to": "2024-09-06_15-01-33",
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "from timestamp not found")
		test.That(t, req, test.ShouldBeNil)
	})

	t.Run("missing to timestamp", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
		}

		req, err := ToSaveCommand(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "to timestamp not found")
		test.That(t, req, test.ShouldBeNil)
	})
}

func TestToFetchCommand(t *testing.T) {
	t.Run("valid command", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
		}

		req, err := ToFetchCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, req.Container, test.ShouldEqual, videostore.ContainerDefault)
	})

	t.Run("valid command with container format", func(t *testing.T) {
		command := map[string]interface{}{
			"from":      "2024-09-06_15-00-33",
			"to":        "2024-09-06_15-01-33",
			"container": "fmp4",
		}

		req, err := ToFetchCommand(command)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, req, test.ShouldNotBeNil)
		test.That(t, req.Container, test.ShouldEqual, videostore.ContainerFMP4)
	})
}

func TestParseTimeRange(t *testing.T) {
	t.Run("valid time range", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
			"to":   "2024-09-06_15-01-33",
		}

		from, to, err := parseTimeRange(command)
		test.That(t, err, test.ShouldBeNil)

		expectedFrom, _ := vsutils.ParseDateTimeString("2024-09-06_15-00-33")
		expectedTo, _ := vsutils.ParseDateTimeString("2024-09-06_15-01-33")
		test.That(t, from.Equal(expectedFrom), test.ShouldBeTrue)
		test.That(t, to.Equal(expectedTo), test.ShouldBeTrue)
	})

	t.Run("invalid from format", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "invalid",
			"to":   "2024-09-06_15-01-33",
		}

		_, _, err := parseTimeRange(command)
		test.That(t, err, test.ShouldNotBeNil)
	})

	t.Run("missing from", func(t *testing.T) {
		command := map[string]interface{}{
			"to": "2024-09-06_15-01-33",
		}

		_, _, err := parseTimeRange(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "from timestamp not found")
	})

	t.Run("missing to", func(t *testing.T) {
		command := map[string]interface{}{
			"from": "2024-09-06_15-00-33",
		}

		_, _, err := parseTimeRange(command)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "to timestamp not found")
	})
}
