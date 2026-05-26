// This package provides the entrypoint for the module
package main

import (
	"context"
	"errors"
	"os"

	cam "github.com/viam-modules/video-store/model/camera"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/utils"
)

func main() {
	utils.ContextualMain(mainWithArgs, module.NewLoggerFromArgs("[video-store-module]"))
}

func mainWithArgs(ctx context.Context, _ []string, logger logging.Logger) error {
	if logger.GetLevel() == logging.DEBUG {
		vsutils.SetLibAVLogLevel("debug")
	} else {
		vsutils.SetLibAVLogLevel("fatal")
	}
	vsutils.SetFFmpegLogCallback(logger)

	// NewModule, not NewModuleFromArgs: latter spawns its own moduleLogger, stranding our ffmpeg sublogger on stdout fallback.
	if len(os.Args) < 2 { //nolint:mnd
		return errors.New("need socket path as command line argument")
	}
	module, err := module.NewModule(ctx, os.Args[1], logger)
	if err != nil {
		return err
	}
	err = module.AddModelFromRegistry(ctx, camera.API, cam.Model)
	if err != nil {
		return err
	}
	err = module.Start(ctx)
	defer module.Close(ctx)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
