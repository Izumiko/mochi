// Package udp implements a BitTorrent tracker via the UDP protocol as
// described in BEP 15.
package udp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sot-tech/mochi/bittorrent"
	"github.com/sot-tech/mochi/frontend"
	"github.com/sot-tech/mochi/middleware"
	"github.com/sot-tech/mochi/pkg/bytepool"
	"github.com/sot-tech/mochi/pkg/conf"
	"github.com/sot-tech/mochi/pkg/log"
	"github.com/sot-tech/mochi/pkg/metrics"
	"github.com/sot-tech/mochi/pkg/timecache"
)

const (
	// Name - registered name of the frontend
	Name                            = "udp"
	defaultKeyLen                   = 32
	maxAllowedClockSkew             = 30 * time.Second
	defaultMaxClockSkew             = 10 * time.Second
	allowedGeneratedPrivateKeyRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
)

var logger = log.NewLogger("frontend/udp")

func init() {
	frontend.RegisterBuilder(Name, NewFrontend)
}

// Config represents all the configurable options for a UDP BitTorrent
// Tracker.
type Config struct {
	frontend.ListenOptions
	PrivateKey   string        `cfg:"private_key"`
	MaxClockSkew time.Duration `cfg:"max_clock_skew"`
	frontend.ParseOptions
}

// Validate sanity checks values set in a config and returns a new config with
// default values replacing anything that is invalid.
func (cfg Config) Validate() (validCfg Config) {
	validCfg = cfg
	validCfg.ListenOptions = cfg.ListenOptions.Validate(logger)

	if cfg.Workers == 0 {
		validCfg.Workers = 1
	}
	if validCfg.Workers > 1 && !validCfg.ReusePort {
		validCfg.ReusePort = true
		logger.Warn().Msg("forcibly enabling ReusePort because Workers > 1")
	}

	// Generate a private key if one isn't provided by the user.
	if cfg.PrivateKey == "" {
		pkeyRunes := make([]byte, defaultKeyLen)
		if _, err := rand.Read(pkeyRunes); err != nil {
			panic(err)
		}
		l := len(allowedGeneratedPrivateKeyRunes)
		for i := range pkeyRunes {
			pkeyRunes[i] = allowedGeneratedPrivateKeyRunes[int(pkeyRunes[i])%l]
		}
		validCfg.PrivateKey = string(pkeyRunes)

		logger.Warn().
			Str("name", "PrivateKey").
			Str("provided", "").
			Str("default", validCfg.PrivateKey).
			Msg("falling back to default configuration")
	}

	// ABS
	sb := cfg.MaxClockSkew >> 63
	validCfg.MaxClockSkew = (cfg.MaxClockSkew ^ sb) + (sb & 1)

	if validCfg.MaxClockSkew == 0 || validCfg.MaxClockSkew > maxAllowedClockSkew {
		validCfg.MaxClockSkew = defaultMaxClockSkew
		logger.Warn().
			Str("name", "MaxClockSkew").
			Dur("provided", cfg.MaxClockSkew).
			Dur("default", validCfg.MaxClockSkew).
			Msg("falling back to default configuration")
	}

	validCfg.ParseOptions = cfg.ParseOptions.Validate(logger)

	return
}

// udpFE holds the state of a UDP BitTorrent Frontend.
type udpFE struct {
	sockets        []*net.UDPConn
	closing        chan any
	wg             sync.WaitGroup
	genPool        *sync.Pool
	logic          *middleware.Logic
	collectTimings bool
	ctxCancel      context.CancelFunc
	onceCloser     sync.Once
	frontend.ParseOptions
}

// NewFrontend builds and starts udp bittorrent frontend from provided configuration
func NewFrontend(c conf.MapConfig, logic *middleware.Logic) (frontend.Frontend, error) {
	var err error
	var cfg Config
	if err = c.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	cfg = cfg.Validate()
	pKey := []byte(cfg.PrivateKey)

	f := &udpFE{
		sockets:        make([]*net.UDPConn, cfg.Workers),
		closing:        make(chan any),
		logic:          logic,
		collectTimings: cfg.EnableRequestTiming,
		ParseOptions:   cfg.ParseOptions,
		genPool: &sync.Pool{
			New: func() any {
				return NewConnectionIDGenerator(pKey, cfg.MaxClockSkew)
			},
		},
	}

	var ctx context.Context
	ctx, f.ctxCancel = context.WithCancel(context.Background())
	logger.Debug().Str("addr", cfg.Addr).Msg("starting listener")
	for i := range f.sockets {
		if f.sockets[i], err = cfg.ListenUDP(); err == nil {
			f.wg.Add(1)
			go func(socket *net.UDPConn, ctx context.Context) {
				if err := f.serve(ctx, socket); err != nil {
					logger.Fatal().Str("addr", cfg.Addr).Err(err).Msg("listener failed")
				} else {
					logger.Info().Str("addr", cfg.Addr).Msg("listener stopped")
				}
			}(f.sockets[i], ctx)
		}
	}
	if err != nil {
		_ = f.Close()
	}

	return f, err
}

// Close provides a thread-safe way to shut down a currently running Frontend.
func (f *udpFE) Close() (err error) {
	f.onceCloser.Do(func() {
		close(f.closing)
		f.ctxCancel()
		cls := make([]io.Closer, 0, len(f.sockets))
		now := time.Now()
		for _, s := range f.sockets {
			if s != nil {
				_ = s.SetDeadline(now)
				cls = append(cls, s)
			}
		}
		f.wg.Wait()
		err = frontend.CloseGroup(cls)
	})

	return
}

// serve blocks while listening and serving UDP BitTorrent requests
// until Stop() is called or an error is returned.
func (f *udpFE) serve(ctx context.Context, socket *net.UDPConn) error {
	pool := bytepool.NewBytePool(2048)
	defer f.wg.Done()

	for {
		// Check to see if we need shutdown.
		select {
		case <-f.closing:
			log.Debug().Msg("serve received shutdown signal")
			return nil
		default:
		}

		// Read a UDP packet into a reusable buffer.
		buffer := pool.Get()
		n, addrPort, err := socket.ReadFromUDPAddrPort(*buffer)
		if err != nil {
			pool.Put(buffer)
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// A temporary failure is not fatal; just pretend it never happened.
				continue
			}
			return err
		}

		// We got nothin'
		if n == 0 {
			pool.Put(buffer)
			continue
		}

		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer pool.Put(buffer)

			// Handle the request.
			addr := addrPort.Addr().Unmap()
			var start time.Time
			if f.collectTimings && metrics.Enabled() {
				start = time.Now()
			}
			action, err := f.handleRequest(ctx,
				Request{(*buffer)[:n], addr},
				ResponseWriter{socket, addrPort},
			)
			if f.collectTimings && metrics.Enabled() {
				recordResponseDuration(action, addr, err, time.Since(start))
			}
		}()
	}
}

// Request represents a UDP payload received by a Tracker.
type Request struct {
	Packet []byte
	IP     netip.Addr
}

// ResponseWriter implements the ability to respond to a Request via the
// io.Writer interface.
type ResponseWriter struct {
	socket   *net.UDPConn
	addrPort netip.AddrPort
}

// Write implements the io.Writer interface for a ResponseWriter.
func (w ResponseWriter) Write(b []byte) (int, error) {
	return w.socket.WriteToUDPAddrPort(b, w.addrPort)
}

// handleRequest parses and responds to a UDP Request.
func (f *udpFE) handleRequest(ctx context.Context, r Request, w ResponseWriter) (actionName string, err error) {
	if len(r.Packet) < 16 {
		// Malformed, no client packets are less than 16 bytes.
		// We explicitly return nothing in case this is a DoS attempt.
		err = errMalformedPacket
		return
	}

	// Parse the headers of the UDP packet.
	connID := r.Packet[0:8]
	actionID := binary.BigEndian.Uint32(r.Packet[8:12])
	txID := r.Packet[12:16]

	// get a connection ID generator/validator from the pool.
	gen := f.genPool.Get().(*ConnectionIDGenerator)
	defer f.genPool.Put(gen)

	// If this isn't requesting a new connection ID and the connection ID is
	// invalid, then fail.
	if actionID != connectActionID && !gen.Validate(connID, r.IP, timecache.Now()) {
		err = errBadConnectionID
		writeErrorResponse(w, txID, err)
		return
	}

	// Handle the requested action.
	switch actionID {
	case connectActionID:
		actionName = "connect"

		if !bytes.Equal(connID, initialConnectionID) {
			err = errMalformedPacket
			return
		}

		writeConnectionID(w, txID, gen.Generate(r.IP, timecache.Now()))

	case announceActionID, announceV6ActionID:
		actionName = "announce"

		var req *bittorrent.AnnounceRequest
		req, err = parseAnnounce(r, actionID == announceV6ActionID, f.ParseOptions)
		if err != nil {
			writeErrorResponse(w, txID, err)
			return
		}

		var resp *bittorrent.AnnounceResponse
		ctx := bittorrent.InjectRouteParamsToContext(ctx, bittorrent.RouteParams{})
		ctx, resp, err = f.logic.HandleAnnounce(ctx, req)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				writeErrorResponse(w, txID, err)
			}
			return
		}

		if err = ctx.Err(); err == nil {
			writeAnnounceResponse(w, txID, resp, actionID == announceV6ActionID, r.IP.Is6())

			ctx = bittorrent.RemapRouteParamsToBgContext(ctx)
			go f.logic.AfterAnnounce(ctx, req, resp)
		}

	case scrapeActionID:
		actionName = "scrape"

		var req *bittorrent.ScrapeRequest
		req, err = parseScrape(r, f.ParseOptions)
		if err != nil {
			writeErrorResponse(w, txID, err)
			return
		}

		var resp *bittorrent.ScrapeResponse
		ctx := bittorrent.InjectRouteParamsToContext(ctx, bittorrent.RouteParams{})
		ctx, resp, err = f.logic.HandleScrape(ctx, req)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				writeErrorResponse(w, txID, err)
			}
			return
		}

		if err = ctx.Err(); err == nil {
			writeScrapeResponse(w, txID, resp)

			ctx = bittorrent.RemapRouteParamsToBgContext(ctx)
			go f.logic.AfterScrape(ctx, req, resp)
		}

	default:
		err = errUnknownAction
		writeErrorResponse(w, txID, err)
	}

	return
}
