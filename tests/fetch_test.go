package videostore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
)

func TestFetchDoCommand(t *testing.T) {
	storageRelativePath := "./video-storage"
	storagePath, err := filepath.Abs(storageRelativePath)
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}
	fullModuleBinPath, err := getModuleBinPath()
	if err != nil {
		t.Fatalf("Failed to get module bin path: %v", err)
	}

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
						"segment_seconds": 30,
						"upload_path": "%s",
						"storage_path": "%s"
					},
					"cam_props": {
						"width": 1920,
						"height": 1080,
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

	// Valid time range. Under grpc limit.
	fetchCmd1 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33",
		"to":      "2024-09-06_15-00-50",
	}

	// Valid time range. Over grpc limit.
	fetchCmd2 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33",
		"to":      "2024-09-06_15-01-33",
	}

	// Invalid time range.
	fetchCmd3 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_14-00-03",
		"to":      "2024-09-06_15-01-33",
	}

	// // Invalid datetime format.
	fetchCmd4 := map[string]interface{}{
		"command": "fetch",
		"from":    "2024-09-06_15-00-33",
		"to":      "2024/09/06 15:01:33",
	}

	t.Run("Test Fetch DoCommand Valid Time Range Under GRPC Limit.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		if err != nil {
			t.Fatalf("failed to setup viam server: %v", err)
		}
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		if err != nil {
			t.Fatalf("failed to get video store component: %v", err)
		}
		res, err := vs.DoCommand(timeoutCtx, fetchCmd1)
		if err != nil {
			t.Fatalf("failed to execute fetch command: %v", err)
		}
		video, ok := res["video"].(string)
		if !ok {
			t.Fatalf("failed to parse video from response: %v", res)
		}
		if video == "" {
			t.Fatalf("video not found in response: %v", res)
		}
	})

	t.Run("Test Fetch DoCommand Valid Time Range Over GRPC Limit.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		if err != nil {
			t.Fatalf("failed to setup viam server: %v", err)
		}
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		if err != nil {
			t.Fatalf("failed to get video store component: %v", err)
		}
		_, err = vs.DoCommand(timeoutCtx, fetchCmd2)
		if err == nil {
			t.Fatalf("expected error but got nil")
		}
	})

	t.Run("Test Fetch DoCommand Invalid Time Range.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		if err != nil {
			t.Fatalf("failed to setup viam server: %v", err)
		}
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		if err != nil {
			t.Fatalf("failed to get video store component: %v", err)
		}
		_, err = vs.DoCommand(timeoutCtx, fetchCmd3)
		if err == nil {
			t.Fatalf("expected error for invalid time range")
		}
	})

	t.Run("Test Fetch DoCommand Invalid Datetime Format.", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		if err != nil {
			t.Fatalf("failed to setup viam server: %v", err)
		}
		defer r.Close(timeoutCtx)
		vs, err := camera.FromRobot(r, videoStoreComponentName)
		if err != nil {
			t.Fatalf("failed to get video store component: %v", err)
		}
		_, err = vs.DoCommand(timeoutCtx, fetchCmd4)
		if err == nil {
			t.Fatalf("expected error for invalid datetime format")
		}
	})
}
