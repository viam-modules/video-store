/*
This example demonstrates calling the fetch command on a video-store resource. To setup:
- You need to have a machine running with the video-store component.
- Ensure you have a .env file with the necessary credentials and secrets.
- Run example script `go run fetch_client.go <camera_name> <start_time> <end_time>`
*/
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot/client"
	"go.viam.com/utils/rpc"
)

func main() {
	logger := logging.NewDebugLogger("video-store-fetch-client")
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	defer machine.Close(ctx)
	logger.Info("Resources:")
	logger.Info(machine.ResourceNames())

	if len(os.Args) < 4 {
		logger.Fatal("Insufficient arguments. Please provide camera_name, start_time, and end_time.")
	}
	cameraName := os.Args[1]
	startTime := os.Args[2]
	endTime := os.Args[3]

	c, err := camera.FromRobot(machine, cameraName)
	if err != nil {
		logger.Error(err)
		return
	}
	resp, err := c.DoCommand(ctx, map[string]interface{}{
		"command": "fetch",
		"from":    startTime,
		"to":      endTime,
	})
	if err != nil {
		logger.Error(err)
		return
	}

	b, err := base64.StdEncoding.DecodeString(resp["video"].(string))
	if err != nil {
		logger.Error(err)
		return
	}

	mp4FileName := fmt.Sprintf("%s_%s-%s.mp4", cameraName, startTime, endTime)
	if err := os.WriteFile(mp4FileName, b, 0o600); err != nil {
		logger.Error(err)
		return
	}
}
