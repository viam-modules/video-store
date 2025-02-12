package videostore

import (
	"context"
	"errors"
	"time"
)

type VideoStore interface {
	Fetch(context.Context, *FetchRequest) (*FetchResponse, error)
	Save(context.Context, *SaveRequest) (*SaveResponse, error)
	Close(context.Context) error
}

type SaveRequest struct {
	From     time.Time
	To       time.Time
	Metadata string
	Async    bool
}

type SaveResponse struct {
	Filename string
}

func (r *SaveRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	return nil
}

type FetchRequest struct {
	From time.Time
	To   time.Time
}

type FetchResponse struct {
	Video []byte
}

func (r *FetchRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	return nil
}
