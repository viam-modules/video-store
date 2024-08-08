# Filtered Video
The `filtered-video` module brings security camera functionality to your smart machine! The module consumes a source [Camera](https://docs.viam.com/components/camera/) and a [Vision Service](https://docs.viam.com/services/vision/), saves the camera output as video files to disk, and filters which video clips are uploaded to the cloud based on triggers from the vision service.

> **Note:** This component is a work in progress and is not yet fully implemented.

## Configure your `filtered-video` component

Fill in the attributes as applicable to the component, according to the example below.

```json
    {
      "name": "fv-cam",
      "namespace": "rdk",
      "type": "camera",
      "model": "viam:camera:filtered-video",
      "attributes": {
        "camera": "webcam-1", // name of the camera to use
        "vision": "vision-service-1", // name of the vision service dependency
        "storage": {
            "clip_seconds": 30,
            "size_gb": 100
        },
        "video": {
            "format": "mp4",
            "codec": "h264",
            "bitrate": 1000000,
            "preset": "medium"
        },
        "objects": {
            "Person": 0.8 // label key and threshold value
        }
      },
      "depends_on": [
        "webcam-1",
        "vision-service-1"
      ]
    }
```

Make sure to configure a [Data Manager Service](https://docs.viam.com/services/data/cloud-sync/) to uplaod video files to the cloud.

```json
    {
      "name": "data_manager-1",
      "namespace": "rdk",
      "type": "data_manager",
      "attributes": {
        "tags": [],
        "additional_sync_paths": [
          "/home/viam/.viam/video-upload/"
        ],
        "capture_disabled": true,
        "sync_interval_mins": 1,
        "capture_dir": ""
      }
    }
```
