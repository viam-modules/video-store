package camera

import (
	"errors"
	"fmt"
	"time"

	"github.com/viam-modules/video-store/videostore"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// parseTimeRange parses the from/to timestamps from a command.
func parseTimeRange(command map[string]interface{}) (from, to time.Time, err error) {
	fromStr, ok := command["from"].(string)
	if !ok {
		return time.Time{}, time.Time{}, errors.New("from timestamp not found")
	}
	from, err = vsutils.ParseDateTimeString(fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toStr, ok := command["to"].(string)
	if !ok {
		return time.Time{}, time.Time{}, errors.New("to timestamp not found")
	}
	to, err = vsutils.ParseDateTimeString(toStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, to, nil
}

// ToSaveCommand converts a do command to a *videostore.SaveRequest.
func ToSaveCommand(command map[string]interface{}) (*videostore.SaveRequest, error) {
	from, to, err := parseTimeRange(command)
	if err != nil {
		return nil, err
	}
	metadata, ok := command["metadata"].(string)
	if !ok {
		metadata = ""
	}
	async, ok := command["async"].(bool)
	if !ok {
		async = false
	}
	// Parse tags array (optional)
	var tags []string
	if tagsIface, ok := command["tags"]; ok {
		switch t := tagsIface.(type) {
		case []interface{}:
			tags = make([]string, 0, len(t))
			for i, v := range t {
				if str, ok := v.(string); ok {
					tags = append(tags, str)
				} else {
					return nil, fmt.Errorf("tag at index %d is not a string", i)
				}
			}
		case []string:
			tags = t
		default:
			return nil, errors.New("tags must be an array of strings")
		}
	}
	return &videostore.SaveRequest{
		From:     from,
		To:       to,
		Metadata: metadata,
		Async:    async,
		Tags:     tags,
	}, nil
}

// ToFetchCommand converts a do command to a *videostore.FetchRequest.
func ToFetchCommand(command map[string]interface{}) (*videostore.FetchRequest, error) {
	from, to, err := parseTimeRange(command)
	if err != nil {
		return nil, err
	}
	container := videostore.ContainerDefault
	if containerStr, ok := command["container"].(string); ok {
		switch containerStr {
		case "mp4":
			container = videostore.ContainerMP4
		case "fmp4":
			container = videostore.ContainerFMP4
		}
	}
	return &videostore.FetchRequest{From: from, To: to, Container: container}, nil
}

func checkDeps(deps resource.Dependencies, config *Config, logger logging.Logger) error {
	// Check for data_manager service dependency.
	// TODO(seanp): Check custom_sync_paths if not using default upload_path in config.
	syncFound := false
	for key, dep := range deps {
		if key.Name == config.Sync {
			if dep.Name().API.Type.String() != "rdk:service" {
				return fmt.Errorf("sync service %s is not a service", config.Sync)
			}
			if dep.Name().API.SubtypeName != "data_manager" {
				return fmt.Errorf("sync service %s is not a data_manager service", config.Sync)
			}
			logger.Debugf("found sync service: %s", key.Name)
			syncFound = true
			break
		}
	}
	if !syncFound {
		return fmt.Errorf("sync service %s not found", config.Sync)
	}

	return nil
}

// GetStorageStateDoCommandResponse converts a StorageState struct to a
// do command response.
func GetStorageStateDoCommandResponse(state *videostore.StorageState) map[string]interface{} {
	diskUsage := map[string]interface{}{
		"storage_used_gb":             float64(state.VideoRanges.StorageUsedBytes) / float64(vsutils.Gigabyte),
		"storage_limit_gb":            float64(state.StorageLimitGB),
		"device_storage_remaining_gb": state.DeviceStorageRemainingGB,
		"storage_path":                state.StoragePath,
	}

	videoList := make([]map[string]interface{}, 0, len(state.VideoRanges.Ranges))
	for _, timeRange := range state.VideoRanges.Ranges {
		fromStr := vsutils.FormatUTC(timeRange.Start)
		toStr := vsutils.FormatUTC(timeRange.End)
		videoList = append(videoList, map[string]interface{}{
			"from": fromStr,
			"to":   toStr,
		})
	}

	return map[string]interface{}{
		"disk_usage":   diskUsage,
		"stored_video": videoList,
	}
}
