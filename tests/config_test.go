package videostore_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
	_ "go.viam.com/rdk/components/camera/fake" // Register the fake camera model
	"go.viam.com/rdk/config"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot"
	robotimpl "go.viam.com/rdk/robot/impl"
	_ "go.viam.com/rdk/services/datamanager/builtin" // Register the data manager service
	"go.viam.com/test"
)

const (
	moduleBinPath           = "bin/video-store"
	videoStoreComponentName = "video-store-1"
	testStoragePath         = "/tmp/video-storage"
	testUploadPath          = "/tmp/video-upload"
	artifactStoragePath     = "../.artifact/data"
)

func setupViamServer(ctx context.Context, configStr string) (robot.Robot, error) {
	cleanVideoStoreDir()
	logger := logging.NewLogger("video-store-module")

	cfg, err := config.FromReader(ctx, "default.json", bytes.NewReader([]byte(configStr)), logger, nil)
	if err != nil {
		logger.Error("failed to parse config", err)
		return nil, err
	}

	r, err := robotimpl.RobotFromConfig(ctx, cfg, nil, logger)
	if err != nil {
		logger.Error("failed to create robot", err)
		return nil, err
	}
	return r, nil
}

func getModuleBinPath() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	fullModuleBinPath := filepath.Join(currentDir, "..", moduleBinPath)
	return fullModuleBinPath, nil
}

func cleanVideoStoreDir() error {
	currentDir, err := os.Getwd()
	if err != nil {
		return err
	}
	videoStoreDir := filepath.Join(currentDir, artifactStoragePath)
	err = os.Chdir(videoStoreDir)
	if err != nil {
		return err
	}
	defer os.Chdir(currentDir) // Ensure we change back to the original directory
	cmd := exec.Command("artifact", "clean")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func testVideoPlayback(t *testing.T, videoPath string) {
	_, err := os.Stat(videoPath)
	test.That(t, err, test.ShouldBeNil)
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", videoPath, "-f", "null", "-")
	err = cmd.Run()
	test.That(t, err, test.ShouldBeNil)
}

func testVideoDuration(t *testing.T, videoPath string, expectedDuration float64) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", videoPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	test.That(t, err, test.ShouldBeNil)
	durationStr := out.String()
	var duration float64
	_, err = fmt.Sscanf(durationStr, "%f", &duration)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, duration, test.ShouldAlmostEqual, expectedDuration)
}

func TestModuleConfiguration(t *testing.T) {
	fullModuleBinPath, err := getModuleBinPath()
	test.That(t, err, test.ShouldBeNil)

	// Full configuration
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
	}`, videoStoreComponentName, testStoragePath, testUploadPath, fullModuleBinPath)

	// no camera specified
	config2 := fmt.Sprintf(`
    {
        "components": [
            {
                "name": "video-store-1",
                "namespace": "rdk",
                "type": "camera",
                "model": "viam:video:storage",
                "attributes": {
					"sync": "data_manager-1",
                    "storage": {
                        "size_gb": 10,
                        "segment_seconds": 30
                    }
                },
				"depends_on": [
					"data_manager-1"
				]
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
    }`, fullModuleBinPath)

	// Storage NOT specified
	config3 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"sync": "data_manager-1",
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
	}`, fullModuleBinPath)

	// size_gb NOT specified
	config4 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"sync": "data_manager-1",
					"storage": {
						"segment_seconds": 30,
						"upload_path": "/tmp/video-upload",
						"storage_path": "/tmp/video-storage"
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
	}`, fullModuleBinPath)

	// framerate NOT specified
	config5 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"sync": "data_manager-1",
					"storage": {
						"size_gb": 10,
						"segment_seconds": 30,
						"upload_path": "/tmp",
						"storage_path": "/tmp"
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
	}`, fullModuleBinPath)

	// data_manager NOT specified
	config6 := fmt.Sprintf(`
	{
		"components": [
			{
				"name": "video-store-1",
				"namespace": "rdk",
				"type": "camera",
				"model": "viam:video:storage",
				"attributes": {
					"camera": "fake-cam-1",
					"storage": {
						"size_gb": 10,
						"segment_seconds": 30,
						"upload_path": "/tmp",
						"storage_path": "/tmp"
					},
					"framerate": 30,
					"video": {
						"codec": "h264",
						"bitrate": 1000000,
						"preset": "ultrafast",
						"format": "mp4"
					},
					"sync": "sync-service"
				},
				"depends_on": [
					"fake-cam-1"
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
		"modules": [
			{
				"type": "local",
				"name": "video-storage",
				"executable_path": "%s",
				"log_level": "debug"
			}
		]
	}`, fullModuleBinPath)

	// Implicit camera dependency
	config7 := fmt.Sprintf(`
		{
			"components": [
				{
					"name": "video-store-1",
					"namespace": "rdk",
					"type": "camera",
					"model": "viam:video:storage",
					"attributes": {
						"camera": "fake-cam-1",
						"sync": "data_manager-1",
						"storage": {
							"size_gb": 10,
							"segment_seconds": 30,
							"upload_path": "/tmp",
							"storage_path": "/tmp"
						},
						"video": {
							"preset": "ultrafast"
						}
					},
					"depends_on": [
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
		}`, fullModuleBinPath)

	t.Run("Valid Configuration Successful", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config1)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
	})

	// no camera specified
	// we want this to succeed to accept do-command requests
	t.Run("Succeeds Configuration No Camera", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config2)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		cam, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, cam, test.ShouldNotBeNil)
	})

	// no storage specified
	t.Run("Fails Configuration No Storage", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config3)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		cam, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, cam, test.ShouldBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "storage")
	})

	t.Run("Fails Configuration No SizeGB", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config4)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		cam, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, cam, test.ShouldBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "size_gb")
	})

	t.Run("No framerate Succeeds", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config5)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("Fails Configuration No DataManager", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config6)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		cam, err := camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, cam, test.ShouldBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "sync")
	})

	t.Run("Implicit Camera Dependency Succeeds", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := setupViamServer(timeoutCtx, config7)
		test.That(t, err, test.ShouldBeNil)
		defer r.Close(timeoutCtx)
		_, err = camera.FromRobot(r, videoStoreComponentName)
		test.That(t, err, test.ShouldBeNil)
	})
}
