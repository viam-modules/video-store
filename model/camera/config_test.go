package camera

import (
	"testing"

	"go.viam.com/test"
)

func TestToFrameVideoStoreVideoConfig_SourceName(t *testing.T) {
	t.Run("SourceName is set when provided", func(t *testing.T) {
		sourceName := "color"
		config := &Config{
			SourceName: sourceName,
			Sync:       "data_manager-1",
			Storage: Storage{
				SizeGB: 10,
			},
		}

		result, err := ToFrameVideoStoreVideoConfig(config, "test-camera", nil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, result.SourceName, test.ShouldNotBeNil)
		test.That(t, *result.SourceName, test.ShouldEqual, sourceName)
	})

	t.Run("SourceName is nil when empty string provided", func(t *testing.T) {
		config := &Config{
			SourceName: "",
			Sync:       "data_manager-1",
			Storage: Storage{
				SizeGB: 10,
			},
		}

		result, err := ToFrameVideoStoreVideoConfig(config, "test-camera", nil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, result.SourceName, test.ShouldBeNil)
	})

	t.Run("SourceName is nil when not provided", func(t *testing.T) {
		config := &Config{
			Sync: "data_manager-1",
			Storage: Storage{
				SizeGB: 10,
			},
		}

		result, err := ToFrameVideoStoreVideoConfig(config, "test-camera", nil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, result.SourceName, test.ShouldBeNil)
	})

	t.Run("SourceName pointer points to correct value", func(t *testing.T) {
		sourceName := "depth"
		config := &Config{
			SourceName: sourceName,
			Sync:       "data_manager-1",
			Storage: Storage{
				SizeGB: 10,
			},
		}

		result, err := ToFrameVideoStoreVideoConfig(config, "test-camera", nil)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, result.SourceName, test.ShouldNotBeNil)

		// Verify the pointer points to the correct value
		actualSourceName := *result.SourceName
		test.That(t, actualSourceName, test.ShouldEqual, sourceName)

		// Verify it's a different memory location (not just pointing to config.SourceName)
		// This ensures the value is properly copied, not just referenced
		test.That(t, &actualSourceName, test.ShouldNotEqual, &config.SourceName)
	})
}
