# Video Storage
The `video-store` module brings security camera functionality to your smart machine! The module consumes a source [Camera](https://docs.viam.com/components/camera/) and saves the output as video files on disk. You can then upload video slices to the cloud using the [save](#save) command, or request the video bytes directly using the [fetch](#fetch) command.

> **Note:** This component is a work in progress and is not yet fully implemented.

## Configure your `video-store` component

Fill in the attributes as applicable to the component, according to the template below.

```json
    {
      "name": <video_store_component_name>,
      "namespace": "rdk",
      "type": "camera",
      "model": "viam:video:storage",
      "attributes": {
        "camera": <camera_component_name>, [required]
        "sync": <data_manager_service_name>, [required]
        "storage": { [required]
            "segment_seconds": <length_of_video_segments>, [optional]
            "size_gb": <total_storage_max_size>, [required]
            "storage_path": <custom_path_to_store_video_files>, [optional]
            "upload_path": <custom_path_to_upload_video_files>, [optional]
        },
        "video": { [optional]
            "format": <video_format>, [optional]
            "codec": <video_codec>, [optional]
            "bitrate": <bits_pers_second>, [optional]
            "preset": <video_preset>, [optional]
        },
        "cam_props": { [required]
            "width": <pixel_width>, [required]
            "height": <pixel_height>, [required]
            "framerate": <frames_per_second>, [required]
        },
      },
      "depends_on": [
        <camera_component_name>, [required]
        <data_manager_service_name> [required]
      ]
    }
```

Make sure to configure a [Data Manager Service](https://docs.viam.com/services/data/cloud-sync/) to uplaod video files to the cloud.

```json
    {
      "name": <data_manager_service_name>,
      "namespace": "rdk",
      "type": "data_manager",
      "attributes": {
        "tags": [],
        "additional_sync_paths": [
          <custom_path_to_upload_video_files>
        ],
        "capture_disabled": true,
        "sync_interval_mins": <sync_interval_minutes>,
        "capture_dir": ""
      }
    }
```

## Do Commands

### `save`

#### Save Request
```json
{
  "command": "save",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp>, [required]
  "metadata": <arbitrary_metadata_string> [optional]
}
```

#### Save Response
```json
{
  "command": "save",
  "filename": <filename_to_be_uploaded>
}
```

### `fetch`

#### Fetch Request
```json
{
  "command": "fetch",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp> [required]
}
```

#### Fetch Response
```json
{
  "command": "fetch",
  "video": <video_bytes>
}
```