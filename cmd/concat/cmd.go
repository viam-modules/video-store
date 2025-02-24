// package main concatinates video files in a storage directory
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/viam-modules/video-store/model/camera"
	"github.com/viam-modules/video-store/videostore"
	"go.viam.com/rdk/logging"
)

func main() {
	videostore.SetLibAVLogLevel("debug")
	ctx := context.Background()
	logger := logging.NewLogger(os.Args[0])
	c := camera.Config{Storage: camera.Storage{SizeGB: 1}}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		logger.Fatal(err.Error())
	}
	rawReq := map[string]interface{}{}
	if err := json.Unmarshal(b, &rawReq); err != nil {
		logger.Fatal(err.Error())
	}

	config, err := camera.ToFrameVideoStoreVideoConfig(&c, os.Args[2], nil)
	if err != nil {
		logger.Fatal(err.Error())
	}
	vs, err := videostore.NewH264RTPVideoStore(ctx, config, logger)
	if err != nil {
		logger.Fatal(err.Error())
	}

	req, err := camera.ToFetchCommand(rawReq)
	if err != nil {
		logger.Fatal(err.Error())
	}
	res, err := vs.Fetch(ctx, req)
	if err != nil {
		logger.Fatal(err.Error())
	}

	//nolint:mnd
	if err := os.WriteFile(os.Args[2]+".mp4", res.Video, 0o600); err != nil {
		logger.Fatal(err.Error())
	}
}
