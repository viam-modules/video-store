package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	var vs videostore.VideoStore
	switch os.Args[3] {
	case "frame":
		vs, err = videostore.NewFramePollingVideoStore(ctx, config, logger)
		if err != nil {
			logger.Fatal(err.Error())
		}
	case "rtp":
		config.Type = videostore.SourceTypeH264RTPPacket
		vs, err = videostore.NewH264RTPVideoStore(ctx, config, logger)
		if err != nil {
			logger.Fatal(err.Error())
		}
	default:
		logger.Fatal("invalid arg 3, options: frame|rtp")
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
	if err := os.WriteFile(fmt.Sprintf("%s_%s.mp4", os.Args[2], os.Args[3]), res.Video, 0o600); err != nil {
		logger.Fatal(err.Error())
	}
}
