package main

import (
	"context"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot/client"
	"go.viam.com/utils/rpc"
)

func main() {
	logger := logging.NewDebugLogger("client")
	err := godotenv.Load()
	if err != nil {
		logger.Fatal("Error loading .env file")
	}
	apiKey := os.Getenv("API_KEY")
	payload := os.Getenv("PAYLOAD")
	machineURL := os.Getenv("MACHINE_URL")
	machine, err := client.New(
		context.Background(),
		machineURL,
		logger,
		client.WithDialOptions(rpc.WithEntityCredentials(
			apiKey,
			rpc.Credentials{
				Type:    rpc.CredentialsTypeAPIKey,
				Payload: payload,
			})),
	)
	if err != nil {
		logger.Fatal(err)
	}

	defer machine.Close(context.Background())
	logger.Info("Resources:")
	logger.Info(machine.ResourceNames())

	// wc-cam
	wcCam, err := camera.FromRobot(machine, "wc-cam")
	if err != nil {
		logger.Error(err)
		return
	}
	wcCamReturnValue, err := wcCam.Properties(context.Background())
	if err != nil {
		logger.Error(err)
		return
	}
	logger.Infof("wc-cam Properties return value: %+v", wcCamReturnValue)

	// fv-cam
	fvCam, err := camera.FromRobot(machine, "fv-cam")
	if err != nil {
		logger.Error(err)
		return
	}
	fvCamReturnValue, err := fvCam.Properties(context.Background())
	if err != nil {
		logger.Error(err)
		return
	}
	logger.Infof("fv-cam Properties return value: %+v", fvCamReturnValue)

	// create a ticker that fires every second
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// loop once a second
	for {
		select {
		case <-context.Background().Done():
			return
		case <-ticker.C:
			logger.Info("looping")
			// hit do_command of fv-cam
			ret, err := fvCam.DoCommand(context.Background(), map[string]interface{}{})
			if err != nil {
				continue
			}
			logger.Infof("fv-cam DoCommand return value: %+v", ret)
			bytes := ret["video"]
			logger.Infof("length of bytes: %d", len(bytes.([]byte)))
		}
	}

}
