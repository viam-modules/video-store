/*
This example demonstrates calling async save command on video-store resource. To setup:
- You need to have a robot running with video-store component.
- Ensure you have a .env file with the necessary credentials and secrets.
- Run example script `go run save_client.go`
*/

package main

import (
	"context"
	"math/rand"
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
		logger.Fatal("Please make sure you add a .env file with the necessary credentials and secrets.")
	}
	robotAddress := os.Getenv("ROBOT_ADDRESS")
	apiKeyID := os.Getenv("API_KEY_ID")
	apiKey := os.Getenv("API_KEY")
	if robotAddress == "" || apiKeyID == "" || apiKey == "" {
		logger.Fatal("Missing required environment variables: ROBOT_ADDRESS, API_KEY_ID, or API_KEY.")
	}

	machine, err := client.New(
		context.Background(),
		robotAddress,
		logger,
		client.WithDialOptions(rpc.WithEntityCredentials(
			apiKeyID,
			rpc.Credentials{
				Type:    rpc.CredentialsTypeAPIKey,
				Payload: apiKey,
			})),
	)
	if err != nil {
		logger.Fatal(err)
	}
	defer machine.Close(context.Background())
	logger.Info("Resources:")
	logger.Info(machine.ResourceNames())

	videoStore, err := camera.FromRobot(machine, "video-store")
	if err != nil {
		logger.Error(err)
		return
	}
	videoStoreReturnValue, err := videoStore.Properties(context.Background())
	if err != nil {
		logger.Error(err)
		return
	}
	logger.Infof("video-store Properties return value: %+v", videoStoreReturnValue)

	// Save clip of random duration every 30 seconds
	for {
		now := time.Now()
		randomSeconds := rand.Intn(56) + 5 // 5 to 60 seconds
		from := now.Add(-time.Duration(randomSeconds) * time.Second)
		nowStr := now.Format("2006-01-02_15-04-05")
		fromStr := from.Format("2006-01-02_15-04-05")
		_, err = videoStore.DoCommand(context.Background(),
			map[string]interface{}{
				"command":  "save",
				"from":     fromStr,
				"to":       nowStr,
				"metadata": "metadata",
				"async":    true,
			},
		)
		if err != nil {
			logger.Error(err)
			return
		}
		time.Sleep(30 * time.Second)
	}

}
