package filteredvideo

import (
	"errors"
	"time"

	"go.viam.com/rdk/logging"
)

// Player is a video player that plays video from a specified time range.
type Player struct {
	logger  logging.Logger
	running bool
	paused  bool
}

// newPlayer creates a new video player.
func newPlayer(logger logging.Logger) *Player {
	return &Player{
		logger: logger,
	}
}

// start start the video player with specified from and to time.
func (p *Player) start(from, to time.Time) error {
	if p.running {
		return errors.New("player already running")
	}

	p.logger.Debug("starting player")
	p.running = true

	// TODO: Validate From and To time.
	// check if start is less than end time
	// check if start is ahead of minimum time of storage
	// check if end is less than end of video storage
	// TODO: Start the player.
	return nil
}

// pause pauses the video player.
func (p *Player) pause() error {
	return nil
}

// stop stops the video player.
func (p *Player) stop() error {
	if !p.running {
		return errors.New("player not running")
	}

	p.logger.Debug("stopping player")
	p.running = false

	return nil
}
