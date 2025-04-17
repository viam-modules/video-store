package videostore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/test"
)

// pathForFetchCmd constructs the .mp4 path based on the "from" field in the command.
func pathForFetchCmd(fromTimestamp string) string {
	cleanedTimestamp := strings.TrimSuffix(fromTimestamp, "Z")
	return "/tmp/" + videoStoreComponentName + "_" + cleanedTimestamp + ".mp4"
}

// assertNoFile checks that the given file path does NOT exist.
// used for testing that the temporary files created by fetch are being properly removed.
func assertNoFile(t *testing.T, filePath string) {
	_, err := os.Stat(filePath)
	test.That(t, os.IsNotExist(err), test.ShouldBeTrue)
}

func TestFetchDoCommand(t *testing.T) {
	storagePath, err := filepath.Abs(artifactStoragePath)
	test.That(t, err, test.ShouldBeNil)
	fullModuleBinPath, err := getModuleBinPath()
	test.That(t, err, test.ShouldBeNil)

	config1 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "%s",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"sync": "data_manager-1",
					"storage": {
						"size_gb": 10,
						"upload_path": "%s",
						"storage_path": "%s"
					},
					"framerate": 30,
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					}
				},
				"depends_on": [
					"fake-cam-1",
					"data_manager-1"
				]
			},
			{
				"name": "fake-cam-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "fake",
				"attributes": {}
			}
		],
		"services": [
			{
				"name": "data_manager-1",
				"namespace": "rdk",
				"type": "data_manager",
				"attributes": {
					"additional_sync_paths": [],
					"capture_disabled": true,
					"sync_interval_mins": 0.1,
					"capture_dir": "",
					"tags": []
				}
			}
		],
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, videoStoreComponentName, testUploadPath, storagePath, fullModuleBinPath)

	// Valid time range. Under grpc limit.
	fetchCmd1 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33",
		"to":      "2024-09-06_15-00-50",
	}

	// Valid time range. Over grpc limit.
	fetchCmd2 := map[string]interface{}{
		"command": "fetch",
		"from":    "2025-03-05_16-36-21",
		"to":      "2025-03-05_16-37-21",
	}

	// Invalid time range.
	fetchCmd3 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_14-00-03",
		"to":      "2024-09-06_15-01-33",
	}

	// Invalid datetime format.
	fetchCmd4 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33",
		"to":      "2024/09/06 15:01:33",
	}

	// Valid UTC time range.
	fetchCmdUTC := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33Z",
		"to":      "2024-09-06_15-00-50Z",
	}

	t.Run("Test Fetch DoCommand Valid Time Range Under GRPC Limit.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		res, err := vs.DoCommand(timeoutCtx, fetchCmd1)
		test.That(t, err, test.ShouldBeNil)
		video, ok := res["video"].(string)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, video, test.ShouldNotBeEmpty)
		filePath := pathForFetchCmd(fetchCmd1["from"].(string))
		assertNoFile(t, filePath)
	})

	t.Run("Test Fetch DoCommand Valid UTC Time Range Under GRPC Limit.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		res, err := vs.DoCommand(timeoutCtx, fetchCmdUTC)
		test.That(t, err, test.ShouldBeNil)
		video, ok := res["video"].(string)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, video, test.ShouldNotBeEmpty)
		filePath := pathForFetchCmd(fetchCmdUTC["from"].(string))
		assertNoFile(t, filePath)
	})

	t.Run("Test Fetch DoCommand Valid Time Range Over GRPC Limit.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = vs.DoCommand(timeoutCtx, fetchCmd2)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "grpc")
		filePath := pathForFetchCmd(fetchCmd2["from"].(string))
		assertNoFile(t, filePath)
	})

	t.Run("Test Fetch DoCommand Invalid Time Range.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = vs.DoCommand(timeoutCtx, fetchCmd3)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "range")
		filePath := pathForFetchCmd(fetchCmd3["from"].(string))
		assertNoFile(t, filePath)
	})

	t.Run("Test Fetch DoCommand Invalid Datetime Format.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = vs.DoCommand(timeoutCtx, fetchCmd4)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "parsing time")
		filePath := pathForFetchCmd(fetchCmd4["from"].(string))
		assertNoFile(t, filePath)
	})
}
