// This package provides the entrypoint for the module
package main

import (
	"context"

	videostore "github.com/viam-modules/video-store/cam"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/utils"
)

func main() {
	utils.ContextualMain(mainWithArgs, logging.NewLogger("video-store-module"))
}

func mainWithArgs(ctx context.Context, _ []string, _ logging.Logger) error {
	module, err := module.NewModuleFromArgs(ctx)
	if err != nil {
		return err
	}
	err = module.AddModelFromRegistry(ctx, camera.API, videostore.Model)
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
