# Video Storage
The `video-store` module brings security camera functionality to your smart machine! The module consumes a source [Camera](https://docs.viam.com/components/camera/) and saves the camera output as video files on disk. You can then later request to upload video slices to the cloud using the [save](#save) command, or request the video bytes directly using the [fetch](#fetch) command.

> **Note:** This component is a work in progress and is not yet fully implemented.

## Configure your `video-store` component

Fill in the attributes as applicable to the component, according to the example below.

```json
    {
      "name": "video-store-1",
      "namespace": "rdk",
      "type": "camera",
      "model": "viam:video:storage",
      "attributes": {
        "camera": "webcam-1", 
        "vision": "vision-service-1", 
        "storage": {
            "segment_seconds": 30,
            "size_gb": 100
        },
        "video": {
            "format": "mp4",
            "codec": "h264",
            "bitrate": 1000000,
            "preset": "medium"
        },
        "cam_props": { 
            "width": 640,
            "height": 480,
            "framerate": 30
        },
      },
      "depends_on": [
        "webcam-1",
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
        "additional_sync_paths": [],
        "capture_disabled": true,
        "sync_interval_mins": 1,
        "capture_dir": ""
      }
    }
```

## Commands

### `save`
```json
{
  "command": "save",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp>, [required]
  "metadata": <arbitrary_metadata_string> [optional]
}
```

### `fetch`
```json
{
  "command": "fetch",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp>, [required]
}
```
