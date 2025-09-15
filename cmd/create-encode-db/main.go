package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image/jpeg"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/robot/client"
	"go.viam.com/rdk/utils"
	"go.viam.com/utils/rpc"
)

// This function produces the sqlite3.db you can use to feed in to encoder-c
func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Please make sure you add a .env file with the necessary credentials and secrets.")
		os.Exit(1)
	}
	robotAddress := os.Getenv("ROBOT_ADDRESS")
	apiKeyID := os.Getenv("API_KEY_ID")
	apiKey := os.Getenv("API_KEY")
	if robotAddress == "" || apiKeyID == "" || apiKey == "" {
		fmt.Println("Missing required environment variables: ROBOT_ADDRESS, API_KEY_ID, or API_KEY.")
		os.Exit(1)
	}

	logger := logging.NewDebugLogger("video-store client")
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
	if len(os.Args) != 3 {
		fmt.Printf("usage: %s <camera name> <fps>", os.Args[0])
		os.Exit(1)
	}
	dbPath := os.Args[1] + "-" + os.Args[2] + "-fps" + ".db"
	if _, err := os.Stat(dbPath); err == nil {
		os.Remove(dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err = db.Close(); err != nil {
			panic(err)
		}
	}()

	fps, err := strconv.ParseInt(os.Args[2], 10, 62)
	if err != nil {
		panic(err)
	}

	sqlStmt := `
    CREATE TABLE images(id INTEGER NOT NULL PRIMARY KEY, data BLOB, unixMicro INTEGER, width INTEGER, height INTEGER);
	`

	if _, err = db.Exec(sqlStmt); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	defer machine.Close(ctx)
	logger.Info(machine.ResourceNames())
	c, err := camera.FromProvider(machine, os.Args[1])
	if err != nil {
		logger.Error(err)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(time.Second / time.Duration(fps))
		resp, _, err := c.Images(ctx, nil, nil)
		if err != nil {
			logger.Info("ignoring error : " + err.Error())
			continue
		}
		if len(resp) == 0 {
			logger.Info("no images found")
			continue
		}
		if len(resp) != 1 {
			logger.Info("expected 1 image received " + strconv.Itoa(len(resp)))
			continue
		}
		namedImage := resp[0]

		mimeType := resp[0].MimeType()

		if mimeType != utils.MimeTypeJPEG {
			logger.Errorf("expected %s mime type got %s", utils.MimeTypeJPEG, mimeType)
			return
		}

		imageBytes, err := namedImage.Bytes(ctx)
		if err != nil {
			logger.Info("ignoring error while getting image bytes: " + err.Error())
			continue
		}
		im, err := jpeg.Decode(bytes.NewReader(imageBytes))
		if err != nil {
			panic(err)
		}
		_, err = db.Exec("INSERT INTO images(data, unixMicro, width, height) VALUES(?, ?, ?, ?);", resp, time.Now().UnixMicro(), im.Bounds().Dx(), im.Bounds().Dy())
		if err != nil {
			panic(err)
		}
	}
}
