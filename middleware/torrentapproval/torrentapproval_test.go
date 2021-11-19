package torrentapproval

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/chihaya/chihaya/middleware"
	"gopkg.in/yaml.v2"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chihaya/chihaya/bittorrent"
)

var cases = []struct {
	cfg      middleware.Config
	ih       string
	approved bool
}{
	// Infohash is whitelisted
	{
		middleware.Config{
			Name: "list",
			Options: map[string]interface{}{
				"whitelist": []string{"3532cf2d327fad8448c075b4cb42c8136964a435"},
			},
		},
		"3532cf2d327fad8448c075b4cb42c8136964a435",
		true,
	},
	// Infohash is not whitelisted
	{
		middleware.Config{
			Name: "list",
			Options: map[string]interface{}{
				"whitelist": []string{"3532cf2d327fad8448c075b4cb42c8136964a435"},
			},
		},
		"4532cf2d327fad8448c075b4cb42c8136964a435",
		false,
	},
	// Infohash is not blacklisted
	{
		middleware.Config{
			Name: "list",
			Options: map[string]interface{}{
				"blacklist": []string{"3532cf2d327fad8448c075b4cb42c8136964a435"},
			},
		},
		"4532cf2d327fad8448c075b4cb42c8136964a435",
		true,
	},
	// Infohash is blacklisted
	{
		middleware.Config{
			Name: "list",
			Options: map[string]interface{}{
				"blacklist": []string{"3532cf2d327fad8448c075b4cb42c8136964a435"},
			},
		},
		"3532cf2d327fad8448c075b4cb42c8136964a435",
		false,
	},
}

func TestHandleAnnounce(t *testing.T) {
	for _, tt := range cases {
		t.Run(fmt.Sprintf("testing hash %s", tt.ih), func(t *testing.T) {
			d := driver{}
			cfg, err := yaml.Marshal(tt.cfg)
			require.Nil(t, err)
			h, err := d.NewHook(cfg)
			require.Nil(t, err)

			ctx := context.Background()
			req := &bittorrent.AnnounceRequest{}
			resp := &bittorrent.AnnounceResponse{}

			hashbytes, err := hex.DecodeString(tt.ih)
			require.Nil(t, err)

			hashinfo := bittorrent.NewInfoHash(hashbytes)

			req.InfoHash = hashinfo

			nctx, err := h.HandleAnnounce(ctx, req, resp)
			require.Equal(t, ctx, nctx)
			if tt.approved == true {
				require.NotEqual(t, err, ErrTorrentUnapproved)
			} else {
				require.Equal(t, err, ErrTorrentUnapproved)
			}
		})
	}
}
