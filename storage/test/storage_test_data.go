package test

import (
	"github.com/chihaya/chihaya/bittorrent"
	"net"
)

var (
	testIh1, testIh2 bittorrent.InfoHash
	testPeerId       bittorrent.PeerID
	testData         []hashPeer
	v4Peer, v6Peer   bittorrent.Peer
)

func init() {
	testIh1, _ = bittorrent.NewInfoHash("00000000000000000001")
	testIh2, _ = bittorrent.NewInfoHash("00000000000000000002")
	testPeerId, _ = bittorrent.NewPeerID([]byte("00000000000000000001"))
	testData = []hashPeer{
		{
			testIh1,
			bittorrent.Peer{ID: testPeerId, Port: 1, IP: bittorrent.IP{IP: net.ParseIP("1.1.1.1").To4(), AddressFamily: bittorrent.IPv4}},
		},
		{
			testIh2,
			bittorrent.Peer{ID: testPeerId, Port: 2, IP: bittorrent.IP{IP: net.ParseIP("abab::0001"), AddressFamily: bittorrent.IPv6}},
		},
	}

	v4Peer = bittorrent.Peer{ID: testPeerId, IP: bittorrent.IP{IP: net.ParseIP("99.99.99.99").To4(), AddressFamily: bittorrent.IPv4}, Port: 9994}
	v6Peer = bittorrent.Peer{ID: testPeerId, IP: bittorrent.IP{IP: net.ParseIP("fc00::0001"), AddressFamily: bittorrent.IPv6}, Port: 9996}
}
