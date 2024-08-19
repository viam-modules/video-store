// This package provides the entrypoint for the module
package main

import (
	"context"

	filteredvideo "github.com/seanavery/filteredvideo/cam"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/utils"
)

func main() {
	utils.ContextualMain(mainWithArgs, logging.NewLogger("filtered-video-module"))
}

func mainWithArgs(ctx context.Context, _ []string, logger logging.Logger) error {
	module, err := module.NewModuleFromArgs(ctx, logger)
	if err != nil {
		return err
	}

	err = module.AddModelFromRegistry(ctx, camera.API, filteredvideo.Model)
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
