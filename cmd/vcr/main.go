package main

import (
	"github.com/viam-modules/video-store/vcr"
	"go.viam.com/rdk/logging"
)

func main() {
	r := &vcr.H265Recorder{Dirpath: "./dbs", Logger: logging.NewLogger("h265recorder")}
	if err := r.Init([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}); err != nil {
		panic(err.Error())
	}

	if err := r.Packet([]byte{11, 12, 13}, 66, true); err != nil {
		panic(err)
	}
}
