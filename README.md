# Video Storage
The `video-store` module brings security camera functionality to your smart machine! The module consumes a source [Camera](https://docs.viam.com/components/camera/) and saves the output as video files on disk. You can then upload video slices to the cloud using the [save](#save) command, or request the video bytes directly using the [fetch](#fetch) command.

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

## DoCommands API

### From/To

The `From` and `To` timestamps are used to specify the start and end times for video clips. These timestamps must be provided in a specific datetime format to ensure proper parsing and formatting.

#### Datetime Format

The datetime format used is: `YYYY-MM-DD_HH-MM-SS`

- `YYYY`: Year (e.g., 2023)
- `MM`: Month (e.g., 01 for January)
- `DD`: Day (e.g., 15)
- `HH`: Hour in 24-hour format (e.g., 14 for 2 PM)
- `MM`: Minutes (e.g., 30)
- `SS`: Seconds (e.g., 45)

#### Datetime Example

- `2024-01-15_14-30-45` represents January 15, 2024, at 2:30:45 PM.

### `save`

The save command retreives video from local storage and, uploads the clip to the cloud.

#### Save Request
```json
{
  "command": "save",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp>, [required]
  "metadata": <arbitrary_metadata_string> [optional]
}
```

#### Async Save Request

The async save command performs the same operation as the save command, but does not wait for the operation to complete. Use this command when you want to save video slices that include the current in-progress video storage segment. It will wait for the current segment to finish recording before saving the video slice.

```json
{
  "command": "save",
  "from": <start_timestamp>, [required]
  "to": <end_timestamp>, [required]
  "metadata": <arbitrary_metadata_string>, [optional]
  "async": true [required]
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

The fetch command retrieves video from local storage, and sends the bytes back to the client.

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