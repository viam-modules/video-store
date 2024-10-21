package videostore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/test"
)

func TestSaveDoCommand(t *testing.T) {
	storageRelativePath := "../.artifact/data"
	storagePath, err := filepath.Abs(storageRelativePath)
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
						"segment_seconds": 10,
						"upload_path": "%s",
						"storage_path": "%s"
					},
					"cam_props": {
						"width": 1280,
						"height": 720,
						"framerate": 30
					},
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

	// Valid time range
	saveCmd1 := map[string]interface{}{
		"command":  "save",
		"from":     "2024-09-06_15-00-33",
		"to":       "2024-09-06_15-01-33",
		"metadata": "test-metadata",
	}

	// Invalid time range
	saveCmd2 := map[string]interface{}{
		"command":  "save",
		"from":     "2024-09-06_14-00-03",
		"to":       "2024-09-06_15-01-33",
		"metadata": "test-metadata",
	}

	// Invalid datetime format
	saveCmd3 := map[string]interface{}{
		"command":  "save",
		"from":     "2024-09-06_15-00-33",
		"to":       "2024/09/06 15:01:33",
		"metadata": "test-metadata",
	}

	// Valid async save
	saveCmd4 := map[string]interface{}{
		"command":  "save",
		"from":     "2024-09-06_15-00-33",
		"to":       "2024-09-06_15-01-33",
		"metadata": "test-metadata",
		"async":    true,
	}

	t.Run("Test Save DoCommand Valid Range", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		res, err := vs.DoCommand(timeoutCtx, saveCmd1)
		test.That(t, err, test.ShouldBeNil)
		filename, ok := res["filename"].(string)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, filename, test.ShouldContainSubstring, "test-metadata")
		test.That(t, filename, test.ShouldContainSubstring, "2024-09-06_15-00-33")
		filePath := filepath.Join(testUploadPath, filename)
		testVideoPlayback(t, filePath)
	})

	t.Run("Test Save DoCommand Invalid Range", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = vs.DoCommand(timeoutCtx, saveCmd2)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "range")
	})

	t.Run("Test Save DoCommand Invalid Datetime Format", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = vs.DoCommand(timeoutCtx, saveCmd3)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "parsing time")
	})

	t.Run("Test Save DoCommand Async", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		res, err := vs.DoCommand(timeoutCtx, saveCmd4)
		test.That(t, err, test.ShouldBeNil)
		_, ok := res["filename"].(string)
		test.That(t, ok, test.ShouldBeTrue)
	})

	t.Run("Test leftover concat txt files are cleaned up", func(t *testing.T) {
		leftoverConcatTxtPath := filepath.Join("/tmp", "concat_test1.txt")
		file, err := os.Create(leftoverConcatTxtPath)
		test.That(t, err, test.ShouldBeNil)
		file.Close()
		_, err = os.Stat(leftoverConcatTxtPath)
		test.That(t, os.IsNotExist(err), test.ShouldBeFalse)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		_, err = os.Stat(leftoverConcatTxtPath)
		test.That(t, os.IsNotExist(err), test.ShouldBeTrue)
	})

	t.Run("Test Async Save DoCommand from most recent video segment", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		// Wait for the first video segment to be created.
		time.Sleep(10 * time.Second)
		now := time.Now()
		fromTime := now.Add(-5 * time.Second)
		toTime := now
		fromTimeStr := fromTime.Format("2006-01-02_15-04-05")
		toTimeStr := toTime.Format("2006-01-02_15-04-05")
		saveCmdNow := map[string]interface{}{
			"command":  "save",
			"from":     fromTimeStr,
			"to":       toTimeStr,
			"metadata": "test-metadata",
			"async":    true,
		}
		res, err := vs.DoCommand(timeoutCtx, saveCmdNow)
		test.That(t, err, test.ShouldBeNil)
		_, ok := res["filename"].(string)
		test.That(t, ok, test.ShouldBeTrue)
		// Wait for async save to complete.
		time.Sleep(15 * time.Second)
		filename := fmt.Sprintf("%s_%s_%s.mp4", videoStoreComponentName, fromTimeStr, "test-metadata")
		concatPath := filepath.Join(testUploadPath, filename)
		_, err = os.Stat(concatPath)
		test.That(t, err, test.ShouldBeNil)
		testVideoPlayback(t, concatPath)
	})
}
