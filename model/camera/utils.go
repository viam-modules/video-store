package camera

import (
	"errors"
	"fmt"
	"os"

	"github.com/viam-modules/video-store/videostore"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// getHomeDir returns the home directory of the user.
func getHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home, nil
}

func ToSaveCommand(command map[string]interface{}) (*videostore.SaveRequest, error) {
	fromStr, ok := command["from"].(string)
	if !ok {
		return nil, errors.New("from timestamp not found")
	}
	from, err := videostore.ParseDateTimeString(fromStr)
	if err != nil {
		return nil, err
	}
	toStr, ok := command["to"].(string)
	if !ok {
		return nil, errors.New("to timestamp not found")
	}
	to, err := videostore.ParseDateTimeString(toStr)
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
	return &videostore.SaveRequest{
		From:     from,
		To:       to,
		Metadata: metadata,
		Async:    async,
	}, nil
}

func ToFetchCommand(command map[string]interface{}) (*videostore.FetchRequest, error) {
	fromStr, ok := command["from"].(string)
	if !ok {
		return nil, errors.New("from timestamp not found")
	}
	from, err := videostore.ParseDateTimeString(fromStr)
	if err != nil {
		return nil, err
	}
	toStr, ok := command["to"].(string)
	if !ok {
		return nil, errors.New("to timestamp not found")
	}
	to, err := videostore.ParseDateTimeString(toStr)
	if err != nil {
		return nil, err
	}
	return &videostore.FetchRequest{From: from, To: to}, nil
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
