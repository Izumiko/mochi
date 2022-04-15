package middleware

import (
	"context"
	"time"

	"github.com/sot-tech/mochi/bittorrent"
	"github.com/sot-tech/mochi/frontend"
	"github.com/sot-tech/mochi/pkg/log"
	"github.com/sot-tech/mochi/pkg/stop"
	"github.com/sot-tech/mochi/storage"
)

var _ frontend.TrackerLogic = &Logic{}

// NewLogic creates a new instance of a TrackerLogic that executes the provided
// middleware hooks.
func NewLogic(annInterval, minAnnInterval time.Duration, peerStore storage.Storage, preHooks, postHooks []Hook) *Logic {
	return &Logic{
		announceInterval:    annInterval,
		minAnnounceInterval: minAnnInterval,
		preHooks:            append(preHooks, &responseHook{store: peerStore}),
		postHooks:           append(postHooks, &swarmInteractionHook{store: peerStore}),
	}
}

// Logic is an implementation of the TrackerLogic that functions by
// executing a series of middleware hooks.
type Logic struct {
	announceInterval    time.Duration
	minAnnounceInterval time.Duration
	preHooks            []Hook
	postHooks           []Hook
}

// HandleAnnounce generates a response for an Announce.
func (l *Logic) HandleAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest) (_ context.Context, resp *bittorrent.AnnounceResponse, err error) {
	resp = &bittorrent.AnnounceResponse{
		Interval:    l.announceInterval,
		MinInterval: l.minAnnounceInterval,
		Compact:     req.Compact,
	}
	for _, h := range l.preHooks {
		if ctx, err = h.HandleAnnounce(ctx, req, resp); err != nil {
			return nil, nil, err
		}
	}

	log.Debug("generated announce response", resp)
	return ctx, resp, nil
}

// AfterAnnounce does something with the results of an Announce after it has
// been completed.
func (l *Logic) AfterAnnounce(ctx context.Context, req *bittorrent.AnnounceRequest, resp *bittorrent.AnnounceResponse) {
	var err error
	for _, h := range l.postHooks {
		if ctx, err = h.HandleAnnounce(ctx, req, resp); err != nil {
			log.Error("post-announce hooks failed", log.Err(err))
			return
		}
	}
}

// HandleScrape generates a response for a Scrape.
func (l *Logic) HandleScrape(ctx context.Context, req *bittorrent.ScrapeRequest) (_ context.Context, resp *bittorrent.ScrapeResponse, err error) {
	resp = &bittorrent.ScrapeResponse{
		Files: make([]bittorrent.Scrape, 0, len(req.InfoHashes)),
	}
	for _, h := range l.preHooks {
		if ctx, err = h.HandleScrape(ctx, req, resp); err != nil {
			return nil, nil, err
		}
	}

	log.Debug("generated scrape response", resp)
	return ctx, resp, nil
}

// AfterScrape does something with the results of a Scrape after it has been
// completed.
func (l *Logic) AfterScrape(ctx context.Context, req *bittorrent.ScrapeRequest, resp *bittorrent.ScrapeResponse) {
	var err error
	for _, h := range l.postHooks {
		if ctx, err = h.HandleScrape(ctx, req, resp); err != nil {
			log.Error("post-scrape hooks failed", log.Err(err))
			return
		}
	}
}

// Stop stops the Logic.
//
// This stops any hooks that implement stop.Stopper.
func (l *Logic) Stop() stop.Result {
	stopGroup := stop.NewGroup()
	for _, hook := range l.preHooks {
		stoppable, ok := hook.(stop.Stopper)
		if ok {
			stopGroup.Add(stoppable)
		}
	}

	for _, hook := range l.postHooks {
		stoppable, ok := hook.(stop.Stopper)
		if ok {
			stopGroup.Add(stoppable)
		}
	}

	return stopGroup.Stop()
}
